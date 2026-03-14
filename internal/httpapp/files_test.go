package httpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestFileUploadAndMetadataFlow(t *testing.T) {
	restoreGlobals(t)
	restoreAuthGlobals(t)
	restoreFileGlobals(t)

	tmp := t.TempDir()
	fileStorageDriverEnv = "disk"
	fileStorageDirEnv = tmp
	nowFn = func() time.Time { return time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC) }
	fileReadFullFn = func(r io.Reader, p []byte) (int, error) {
		for i := range p {
			p[i] = byte(i + 1)
		}
		return len(p), nil
	}

	hookCh := make(chan fileRecord, 1)
	fapi := newFileAPI(newMemoryFileRepository(), newBlobStorageFromEnv(), &testHookRecorder{ch: hookCh})

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "doc.txt")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := io.Copy(part, strings.NewReader("hello-upload")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/files", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = req.WithContext(withAuthUserID(req.Context(), 7))
	rec := httptest.NewRecorder()
	fapi.upload(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}

	var uploadResp struct {
		Data fileUploadResponse `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &uploadResp)
	if uploadResp.Data.ID != 1 || uploadResp.Data.SizeBytes != 12 || uploadResp.Data.StorageProvider != "disk" {
		t.Fatalf("upload resp=%+v", uploadResp.Data)
	}
	if uploadResp.Data.SHA256 == "" || uploadResp.Data.StorageKey == "" {
		t.Fatalf("missing hash/key: %+v", uploadResp.Data)
	}

	select {
	case got := <-hookCh:
		if got.ID != uploadResp.Data.ID {
			t.Fatalf("hook file id=%d", got.ID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected hook call")
	}

	storedPath := filepath.Join(tmp, filepath.FromSlash(uploadResp.Data.StorageKey))
	data, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(data) != "hello-upload" {
		t.Fatalf("stored content=%q", data)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/files/1", nil)
	getReq = getReq.WithContext(withAuthUserID(getReq.Context(), 7))
	getRec := httptest.NewRecorder()
	getMux := http.NewServeMux()
	getMux.HandleFunc("GET /files/{id}", fapi.getFileMetadata)
	getMux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
}

func TestFileAPIErrors(t *testing.T) {
	restoreGlobals(t)
	restoreAuthGlobals(t)
	restoreFileGlobals(t)

	auth := newAuthAPI()
	tokens, _ := auth.issueTokens(1, "user")

	fileStorageDriverEnv = "disk"
	fileStorageDirEnv = t.TempDir()
	fileReadFullFn = func(r io.Reader, p []byte) (int, error) {
		for i := range p {
			p[i] = 1
		}
		return len(p), nil
	}

	t.Run("missing auth", func(t *testing.T) {
		h := NewHandlerWithRepositories(newMemoryTaskRepository(), newMemoryShortURLRepository())
		req := httptest.NewRequest(http.MethodPost, "/files", strings.NewReader("x"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("multipart required", func(t *testing.T) {
		h := NewHandlerWithRepositories(newMemoryTaskRepository(), newMemoryShortURLRepository())
		req := httptest.NewRequest(http.MethodPost, "/files", strings.NewReader("x"))
		req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing file part", func(t *testing.T) {
		h := NewHandlerWithRepositories(newMemoryTaskRepository(), newMemoryShortURLRepository())
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		_ = w.WriteField("name", "v")
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/files", &body)
		req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("storage failure", func(t *testing.T) {
		api := newFileAPI(newMemoryFileRepository(), testFailingBlobStorage{err: errors.New("boom")}, noopFileProcessingHook{})
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		p, _ := w.CreateFormFile("file", "x.txt")
		_, _ = p.Write([]byte("x"))
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/files", &body)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		api.upload(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("repo create failure", func(t *testing.T) {
		api := newFileAPI(testFailingFileRepo{createErr: errors.New("db fail")}, newDiskBlobStorage(t.TempDir()), noopFileProcessingHook{})
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		p, _ := w.CreateFormFile("file", "x.txt")
		_, _ = p.Write([]byte("x"))
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/files", &body)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		api.upload(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("file get validations and authz", func(t *testing.T) {
		repo := newMemoryFileRepository()
		_, _ = repo.CreateFile(context.Background(), createFileInput{
			OwnerUserID:     1,
			FileName:        "x.txt",
			ContentType:     "text/plain",
			SizeBytes:       1,
			SHA256:          strings.Repeat("a", 64),
			StorageProvider: "disk",
			StorageKey:      "a/b",
		}, time.Now())

		api := newFileAPI(repo, newDiskBlobStorage(t.TempDir()), noopFileProcessingHook{})
		mux := http.NewServeMux()
		mux.HandleFunc("GET /files/{id}", api.getFileMetadata)
		req := httptest.NewRequest(http.MethodGet, "/files/bad", nil)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/files/999", nil)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/files/1", nil)
		req = req.WithContext(withAuthUserID(req.Context(), 2))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("repo get internal error", func(t *testing.T) {
		api := newFileAPI(testFailingFileRepo{getErr: errors.New("select fail")}, newDiskBlobStorage(t.TempDir()), noopFileProcessingHook{})
		mux := http.NewServeMux()
		mux.HandleFunc("GET /files/{id}", api.getFileMetadata)
		req := httptest.NewRequest(http.MethodGet, "/files/1", nil)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("unauthorized direct handlers", func(t *testing.T) {
		api := newFileAPI(newMemoryFileRepository(), newDiskBlobStorage(t.TempDir()), noopFileProcessingHook{})
		rec := httptest.NewRecorder()
		api.upload(rec, httptest.NewRequest(http.MethodPost, "/files", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("upload status=%d", rec.Code)
		}

		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/files/1", nil)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /files/{id}", api.getFileMetadata)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("get status=%d", rec.Code)
		}
	})

	t.Run("upload filename missing and key generation error", func(t *testing.T) {
		api := newFileAPI(newMemoryFileRepository(), newDiskBlobStorage(t.TempDir()), noopFileProcessingHook{})

		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		p, _ := w.CreateFormField("file")
		_, _ = p.Write([]byte("x"))
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/files", &body)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		api.upload(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}

		fileReadFullFn = func(io.Reader, []byte) (int, error) { return 0, errors.New("rand fail") }
		body.Reset()
		w = multipart.NewWriter(&body)
		pf, _ := w.CreateFormFile("file", "x.txt")
		_, _ = pf.Write([]byte("x"))
		_ = w.Close()
		req = httptest.NewRequest(http.MethodPost, "/files", &body)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec = httptest.NewRecorder()
		api.upload(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("upload empty content type defaults and hook error branch", func(t *testing.T) {
		var logs []string
		loggerPrintfFn = func(format string, args ...any) {
			logs = append(logs, format)
		}
		hook := &testHookRecorder{ch: make(chan fileRecord, 1), err: errors.New("hook failed")}
		api := newFileAPI(newMemoryFileRepository(), newDiskBlobStorage(t.TempDir()), hook)
		fileReadFullFn = func(r io.Reader, p []byte) (int, error) {
			for i := range p {
				p[i] = 1
			}
			return len(p), nil
		}

		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="x.txt"`)
		part, _ := w.CreatePart(header)
		_, _ = part.Write([]byte("hello"))
		_ = w.Close()

		req := httptest.NewRequest(http.MethodPost, "/files", &body)
		req = req.WithContext(withAuthUserID(req.Context(), 1))
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		api.upload(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		select {
		case <-hook.ch:
		case <-time.After(1 * time.Second):
			t.Fatal("expected hook call")
		}
		time.Sleep(10 * time.Millisecond)
		if len(logs) == 0 {
			t.Fatal("expected hook error log")
		}
	})
}

func TestDiskStorageAndHelpers(t *testing.T) {
	restoreFileGlobals(t)

	t.Run("disk storage put and errors", func(t *testing.T) {
		ds := newDiskBlobStorage(t.TempDir())
		if _, _, err := ds.Put(context.Background(), "", strings.NewReader("x"), "text/plain"); err == nil {
			t.Fatal("expected key error")
		}

		fileMkdirAllFn = func(string, os.FileMode) error { return errors.New("mkdir fail") }
		if _, _, err := ds.Put(context.Background(), "a/b.txt", strings.NewReader("x"), "text/plain"); err == nil {
			t.Fatal("expected mkdir error")
		}
		restoreFileGlobals(t)

		fileCreateFn = func(string) (*os.File, error) { return nil, errors.New("create fail") }
		if _, _, err := ds.Put(context.Background(), "a/b.txt", strings.NewReader("x"), "text/plain"); err == nil {
			t.Fatal("expected create error")
		}
		restoreFileGlobals(t)

		if _, _, err := ds.Put(context.Background(), "a/b.txt", errReader{}, "text/plain"); err == nil {
			t.Fatal("expected write error")
		}
	})

	t.Run("disk storage create and write error branches", func(t *testing.T) {
		ds := newDiskBlobStorage(t.TempDir())
		fileMkdirAllFn = os.MkdirAll
		fileCreateFn = func(string) (*os.File, error) { return nil, errors.New("create fail branch") }
		if _, _, err := ds.Put(context.Background(), "a/b.txt", strings.NewReader("x"), "text/plain"); err == nil {
			t.Fatal("expected create error branch")
		}

		fileCreateFn = os.Create
		if _, _, err := ds.Put(context.Background(), "a/b.txt", errReader{}, "text/plain"); err == nil {
			t.Fatal("expected write error branch")
		}
	})

	t.Run("read file part helper", func(t *testing.T) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		_ = w.WriteField("skip", "v")
		p, _ := w.CreateFormFile("file", "x.txt")
		_, _ = p.Write([]byte("x"))
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/files", &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		mr, _ := req.MultipartReader()
		part, err := readFilePart(mr)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		_ = part.Close()

		body.Reset()
		w = multipart.NewWriter(&body)
		_ = w.WriteField("skip", "v")
		_ = w.Close()
		req = httptest.NewRequest(http.MethodPost, "/files", &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		mr, _ = req.MultipartReader()
		if _, err := readFilePart(mr); err == nil {
			t.Fatal("expected missing file part error")
		}

		if _, err := readFilePart(multipart.NewReader(errReader{}, "x")); err == nil {
			t.Fatal("expected read multipart error")
		}
	})

	t.Run("parse id and key generation", func(t *testing.T) {
		if _, err := parseFileID("0"); err == nil {
			t.Fatal("expected parse error")
		}
		if _, err := parseFileID("x"); err == nil {
			t.Fatal("expected parse error")
		}
		if id, err := parseFileID("10"); err != nil || id != 10 {
			t.Fatalf("id=%d err=%v", id, err)
		}

		fileReadFullFn = func(io.Reader, []byte) (int, error) { return 0, errors.New("rand fail") }
		if _, err := newStorageKey("a.txt"); err == nil {
			t.Fatal("expected random error")
		}
		fileReadFullFn = func(r io.Reader, p []byte) (int, error) {
			for i := range p {
				p[i] = 1
			}
			return len(p), nil
		}
		nowFn = func() time.Time { return time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC) }
		key, err := newStorageKey("a.txt")
		if err != nil || !strings.HasSuffix(key, ".txt") || !strings.HasPrefix(key, "20260314T100000/") {
			t.Fatalf("key=%q err=%v", key, err)
		}
	})
}

func TestS3StorageAndEnvSelection(t *testing.T) {
	restoreGlobals(t)
	restoreFileGlobals(t)

	t.Run("s3 put success and failure", func(t *testing.T) {
		up := &mockS3Uploader{}
		s := newS3BlobStorage("bucket", up)
		if got := s.Provider(); got != "s3" {
			t.Fatalf("provider=%q", got)
		}
		key, size, err := s.Put(context.Background(), "a/key.txt", strings.NewReader("abc"), "text/plain")
		if err != nil || key != "a/key.txt" || size != 3 {
			t.Fatalf("key=%q size=%d err=%v", key, size, err)
		}
		if up.lastInput == nil || aws.ToString(up.lastInput.Bucket) != "bucket" || aws.ToString(up.lastInput.Key) != "a/key.txt" {
			t.Fatalf("bad s3 input: %#v", up.lastInput)
		}
		if up.lastInput.ContentType == nil || *up.lastInput.ContentType != "text/plain" {
			t.Fatalf("bad content type: %#v", up.lastInput.ContentType)
		}

		up.err = errors.New("s3 down")
		if _, _, err := s.Put(context.Background(), "a/key.txt", strings.NewReader("abc"), "text/plain"); err == nil {
			t.Fatal("expected upload error")
		}

		empty := newS3BlobStorage("", up)
		if _, _, err := empty.Put(context.Background(), "a", strings.NewReader("a"), "text/plain"); err == nil {
			t.Fatal("expected bucket error")
		}
	})

	t.Run("new blob storage from env branches", func(t *testing.T) {
		fileStorageDriverEnv = ""
		fileStorageDirEnv = t.TempDir()
		if got := newBlobStorageFromEnv().Provider(); got != "disk" {
			t.Fatalf("provider=%q", got)
		}

		fileStorageDriverEnv = "unknown"
		if got := newBlobStorageFromEnv().Provider(); got != "disk" {
			t.Fatalf("provider=%q", got)
		}

		fileStorageDriverEnv = "s3"
		fileS3BucketEnv = "bucket"
		fileS3RegionEnv = "us-east-1"
		fileS3AccessKeyEnv = "x"
		fileS3SecretKeyEnv = "y"
		fileS3PathStyleEnv = true
		if got := newBlobStorageFromEnv().Provider(); got != "s3" {
			t.Fatalf("provider=%q", got)
		}

		fileS3EndpointEnv = "http://localhost:9000"
		if got := newBlobStorageFromEnv().Provider(); got != "s3" {
			t.Fatalf("provider=%q", got)
		}

		loadAWSConfigFn = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
			return aws.Config{}, errors.New("config fail")
		}
		if got := newBlobStorageFromEnv().Provider(); got != "disk" {
			t.Fatalf("provider=%q", got)
		}
	})
}

func TestGetenvAndToFileUploadResponse(t *testing.T) {
	restoreFileGlobals(t)
	if err := (noopFileProcessingHook{}).OnUploaded(context.Background(), fileRecord{}); err != nil {
		t.Fatalf("noop hook err=%v", err)
	}
	if got := getenv("SHOULD_NOT_EXIST_ANYWHERE", "fallback"); got != "fallback" {
		t.Fatalf("got=%q", got)
	}
	_ = os.Setenv("TEST_GETENV_VALUE", "x")
	t.Cleanup(func() { _ = os.Unsetenv("TEST_GETENV_VALUE") })
	if got := getenv("TEST_GETENV_VALUE", "fallback"); got != "x" {
		t.Fatalf("got=%q", got)
	}

	out := toFileUploadResponse(fileRecord{
		ID:              1,
		FileName:        "a.txt",
		ContentType:     "text/plain",
		SizeBytes:       1,
		SHA256:          strings.Repeat("a", 64),
		StorageProvider: "disk",
		StorageKey:      "2026/key.txt",
		CreatedAt:       time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC),
	})
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), "\"created_at\":\"2026-03-14T10:00:00Z\"") {
		t.Fatalf("resp=%s", b)
	}
}

type testFailingFileRepo struct {
	createErr error
	getErr    error
}

func (r testFailingFileRepo) CreateFile(context.Context, createFileInput, time.Time) (fileRecord, error) {
	if r.createErr != nil {
		return fileRecord{}, r.createErr
	}
	return fileRecord{}, nil
}

func (r testFailingFileRepo) GetFile(context.Context, int64) (fileRecord, error) {
	if r.getErr != nil {
		return fileRecord{}, r.getErr
	}
	return fileRecord{}, nil
}

type testFailingBlobStorage struct{ err error }

func (s testFailingBlobStorage) Provider() string { return "disk" }

func (s testFailingBlobStorage) Put(context.Context, string, io.Reader, string) (string, int64, error) {
	return "", 0, s.err
}

type testHookRecorder struct {
	ch  chan fileRecord
	err error
}

func (h *testHookRecorder) OnUploaded(_ context.Context, rec fileRecord) error {
	if h.ch != nil {
		h.ch <- rec
	}
	return h.err
}

type mockS3Uploader struct {
	lastInput *s3.PutObjectInput
	err       error
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read fail") }

func (m *mockS3Uploader) Upload(_ context.Context, input *s3.PutObjectInput, _ ...func(*manager.Uploader)) (*manager.UploadOutput, error) {
	m.lastInput = input
	_, _ = io.ReadAll(input.Body)
	if m.err != nil {
		return nil, m.err
	}
	return &manager.UploadOutput{}, nil
}

func restoreFileGlobals(t *testing.T) {
	t.Helper()
	oldDriver := fileStorageDriverEnv
	oldDir := fileStorageDirEnv
	oldEndpoint := fileS3EndpointEnv
	oldRegion := fileS3RegionEnv
	oldBucket := fileS3BucketEnv
	oldAccess := fileS3AccessKeyEnv
	oldSecret := fileS3SecretKeyEnv
	oldPathStyle := fileS3PathStyleEnv
	oldMkdir := fileMkdirAllFn
	oldCreate := fileCreateFn
	oldReadFull := fileReadFullFn
	oldLoadAWS := loadAWSConfigFn

	t.Cleanup(func() {
		fileStorageDriverEnv = oldDriver
		fileStorageDirEnv = oldDir
		fileS3EndpointEnv = oldEndpoint
		fileS3RegionEnv = oldRegion
		fileS3BucketEnv = oldBucket
		fileS3AccessKeyEnv = oldAccess
		fileS3SecretKeyEnv = oldSecret
		fileS3PathStyleEnv = oldPathStyle
		fileMkdirAllFn = oldMkdir
		fileCreateFn = oldCreate
		fileReadFullFn = oldReadFull
		loadAWSConfigFn = oldLoadAWS
	})
}
