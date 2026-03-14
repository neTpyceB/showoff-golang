package httpapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var (
	errFileNotFound = errors.New("file not found")

	fileStorageDriverEnv = getenv("FILE_STORAGE_DRIVER", "disk")
	fileStorageDirEnv    = getenv("FILE_STORAGE_DIR", "./tmp/uploads")
	fileS3EndpointEnv    = os.Getenv("FILE_S3_ENDPOINT")
	fileS3RegionEnv      = getenv("FILE_S3_REGION", "us-east-1")
	fileS3BucketEnv      = os.Getenv("FILE_S3_BUCKET")
	fileS3AccessKeyEnv   = os.Getenv("FILE_S3_ACCESS_KEY")
	fileS3SecretKeyEnv   = os.Getenv("FILE_S3_SECRET_KEY")
	fileS3PathStyleEnv   = strings.EqualFold(getenv("FILE_S3_PATH_STYLE", "true"), "true")

	fileMkdirAllFn  = os.MkdirAll
	fileCreateFn    = os.Create
	fileReadFullFn  = io.ReadFull
	loadAWSConfigFn = config.LoadDefaultConfig
)

type fileRecord struct {
	ID              int64
	OwnerUserID     int64
	FileName        string
	ContentType     string
	SizeBytes       int64
	SHA256          string
	StorageProvider string
	StorageKey      string
	CreatedAt       time.Time
}

type createFileInput struct {
	OwnerUserID     int64
	FileName        string
	ContentType     string
	SizeBytes       int64
	SHA256          string
	StorageProvider string
	StorageKey      string
}

type fileRepository interface {
	CreateFile(ctx context.Context, in createFileInput, ts time.Time) (fileRecord, error)
	GetFile(ctx context.Context, id int64) (fileRecord, error)
}

type memoryFileRepository struct {
	mu    sync.Mutex
	seq   int64
	items map[int64]fileRecord
}

func newMemoryFileRepository() *memoryFileRepository {
	return &memoryFileRepository{items: map[int64]fileRecord{}}
}

func (r *memoryFileRepository) CreateFile(_ context.Context, in createFileInput, ts time.Time) (fileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	rec := fileRecord{
		ID:              r.seq,
		OwnerUserID:     in.OwnerUserID,
		FileName:        in.FileName,
		ContentType:     in.ContentType,
		SizeBytes:       in.SizeBytes,
		SHA256:          in.SHA256,
		StorageProvider: in.StorageProvider,
		StorageKey:      in.StorageKey,
		CreatedAt:       ts,
	}
	r.items[rec.ID] = rec
	return rec, nil
}

func (r *memoryFileRepository) GetFile(_ context.Context, id int64) (fileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.items[id]
	if !ok {
		return fileRecord{}, errFileNotFound
	}
	return rec, nil
}

type blobStorage interface {
	Provider() string
	Put(ctx context.Context, key string, body io.Reader, contentType string) (storedKey string, sizeBytes int64, err error)
}

type diskBlobStorage struct {
	rootDir string
}

func newDiskBlobStorage(rootDir string) *diskBlobStorage {
	return &diskBlobStorage{rootDir: rootDir}
}

func (s *diskBlobStorage) Provider() string { return "disk" }

func (s *diskBlobStorage) Put(_ context.Context, key string, body io.Reader, _ string) (string, int64, error) {
	if strings.TrimSpace(key) == "" {
		return "", 0, errors.New("key is required")
	}
	path := filepath.Join(s.rootDir, filepath.FromSlash(key))
	if err := fileMkdirAllFn(filepath.Dir(path), 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir upload dir: %w", err)
	}
	f, err := fileCreateFn(path)
	if err != nil {
		return "", 0, fmt.Errorf("create upload file: %w", err)
	}
	defer f.Close()

	n, err := io.Copy(f, body)
	if err != nil {
		return "", 0, fmt.Errorf("write upload file: %w", err)
	}
	return key, n, nil
}

type s3Uploader interface {
	Upload(ctx context.Context, input *s3.PutObjectInput, opts ...func(*manager.Uploader)) (*manager.UploadOutput, error)
}

type s3BlobStorage struct {
	bucket   string
	uploader s3Uploader
}

func newS3BlobStorage(bucket string, uploader s3Uploader) *s3BlobStorage {
	return &s3BlobStorage{bucket: bucket, uploader: uploader}
}

func (s *s3BlobStorage) Provider() string { return "s3" }

func (s *s3BlobStorage) Put(ctx context.Context, key string, body io.Reader, contentType string) (string, int64, error) {
	if strings.TrimSpace(s.bucket) == "" {
		return "", 0, errors.New("s3 bucket is required")
	}
	counter := &countingWriter{}
	tee := io.TeeReader(body, counter)

	_, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        tee,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", 0, fmt.Errorf("upload object to s3: %w", err)
	}
	return key, counter.n, nil
}

type countingWriter struct{ n int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

type fileProcessingHook interface {
	OnUploaded(ctx context.Context, rec fileRecord) error
}

type noopFileProcessingHook struct{}

func (noopFileProcessingHook) OnUploaded(context.Context, fileRecord) error { return nil }

type fileAPI struct {
	repo    fileRepository
	storage blobStorage
	hook    fileProcessingHook
}

func newFileAPI(repo fileRepository, storage blobStorage, hook fileProcessingHook) *fileAPI {
	return &fileAPI{repo: repo, storage: storage, hook: hook}
}

type fileUploadResponse struct {
	ID              int64  `json:"id"`
	FileName        string `json:"file_name"`
	ContentType     string `json:"content_type"`
	SizeBytes       int64  `json:"size_bytes"`
	SHA256          string `json:"sha256"`
	StorageProvider string `json:"storage_provider"`
	StorageKey      string `json:"storage_key"`
	CreatedAt       string `json:"created_at"`
}

func (a *fileAPI) upload(w http.ResponseWriter, r *http.Request) {
	userID, ok := authUserIDFromContext(r.Context())
	if !ok {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{
			Code:    "unauthorized",
			Message: "authentication required",
		})
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "bad_request",
			Message: "request must be multipart/form-data",
		})
		return
	}

	part, err := readFilePart(mr)
	if err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "bad_request",
			Message: err.Error(),
		})
		return
	}
	defer part.Close()

	if strings.TrimSpace(part.FileName()) == "" {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "bad_request",
			Message: "file part must include a filename",
		})
		return
	}

	key, err := newStorageKey(part.FileName())
	if err != nil {
		respondErrorJSON(w, r, http.StatusInternalServerError, apiError{
			Code:    "internal_error",
			Message: "failed to allocate storage key",
		})
		return
	}

	contentType := strings.TrimSpace(part.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	hasher := sha256.New()
	storedKey, sizeBytes, err := a.storage.Put(r.Context(), key, io.TeeReader(part, hasher), contentType)
	if err != nil {
		respondErrorJSON(w, r, http.StatusInternalServerError, apiError{
			Code:    "internal_error",
			Message: "failed to store file",
		})
		return
	}

	rec, err := a.repo.CreateFile(r.Context(), createFileInput{
		OwnerUserID:     userID,
		FileName:        part.FileName(),
		ContentType:     contentType,
		SizeBytes:       sizeBytes,
		SHA256:          hex.EncodeToString(hasher.Sum(nil)),
		StorageProvider: a.storage.Provider(),
		StorageKey:      storedKey,
	}, nowFn().UTC())
	if err != nil {
		respondErrorJSON(w, r, http.StatusInternalServerError, apiError{
			Code:    "internal_error",
			Message: "failed to persist file metadata",
		})
		return
	}

	go func(rec fileRecord) {
		if err := a.hook.OnUploaded(context.Background(), rec); err != nil {
			loggerPrintfFn("file upload hook error file_id=%d err=%v", rec.ID, err)
		}
	}(rec)

	respondJSON(w, r, http.StatusCreated, toFileUploadResponse(rec))
}

func (a *fileAPI) getFileMetadata(w http.ResponseWriter, r *http.Request) {
	userID, ok := authUserIDFromContext(r.Context())
	if !ok {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{
			Code:    "unauthorized",
			Message: "authentication required",
		})
		return
	}

	id, err := parseFileID(r.PathValue("id"))
	if err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "bad_request",
			Message: err.Error(),
		})
		return
	}

	rec, err := a.repo.GetFile(r.Context(), id)
	if err != nil {
		if errors.Is(err, errFileNotFound) {
			respondErrorJSON(w, r, http.StatusNotFound, apiError{
				Code:    "not_found",
				Message: "file not found",
			})
			return
		}
		respondErrorJSON(w, r, http.StatusInternalServerError, apiError{
			Code:    "internal_error",
			Message: "failed to fetch file metadata",
		})
		return
	}

	if rec.OwnerUserID != userID {
		respondErrorJSON(w, r, http.StatusForbidden, apiError{
			Code:    "forbidden",
			Message: "you do not have access to this file",
		})
		return
	}

	respondJSON(w, r, http.StatusOK, toFileUploadResponse(rec))
}

func toFileUploadResponse(rec fileRecord) fileUploadResponse {
	return fileUploadResponse{
		ID:              rec.ID,
		FileName:        rec.FileName,
		ContentType:     rec.ContentType,
		SizeBytes:       rec.SizeBytes,
		SHA256:          rec.SHA256,
		StorageProvider: rec.StorageProvider,
		StorageKey:      rec.StorageKey,
		CreatedAt:       rec.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func parseFileID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid file id")
	}
	return id, nil
}

func readFilePart(mr *multipart.Reader) (*multipart.Part, error) {
	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New(`multipart form must include "file" part`)
			}
			return nil, errors.New("failed to read multipart form")
		}
		if part.FormName() == "file" {
			return part, nil
		}
		_ = part.Close()
	}
}

func newStorageKey(fileName string) (string, error) {
	var buf [12]byte
	if _, err := fileReadFullFn(rand.Reader, buf[:]); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	ts := nowFn().UTC().Format("20060102T150405")
	return fmt.Sprintf("%s/%s%s", ts, hex.EncodeToString(buf[:]), ext), nil
}

func newBlobStorageFromEnv() blobStorage {
	driver := strings.ToLower(strings.TrimSpace(fileStorageDriverEnv))
	if driver == "" || driver == "disk" {
		return newDiskBlobStorage(fileStorageDirEnv)
	}
	if driver != "s3" {
		loggerPrintfFn("unknown FILE_STORAGE_DRIVER=%q, fallback to disk", driver)
		return newDiskBlobStorage(fileStorageDirEnv)
	}

	cfg, err := loadAWSConfigFn(context.Background(),
		config.WithRegion(fileS3RegionEnv),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(fileS3AccessKeyEnv, fileS3SecretKeyEnv, "")),
	)
	if err != nil {
		loggerPrintfFn("s3 config error: %v; fallback to disk", err)
		return newDiskBlobStorage(fileStorageDirEnv)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if strings.TrimSpace(fileS3EndpointEnv) != "" {
			o.BaseEndpoint = aws.String(fileS3EndpointEnv)
		}
		o.UsePathStyle = fileS3PathStyleEnv
	})
	uploader := manager.NewUploader(client)
	return newS3BlobStorage(fileS3BucketEnv, uploader)
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func castFileRepositoryOrMemory(repo any) fileRepository {
	if r, ok := repo.(fileRepository); ok {
		return r
	}
	return newMemoryFileRepository()
}
