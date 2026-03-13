package httpapp

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const shortURLCodeLength = 8
const shortURLAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var (
	errShortURLNotFound     = errors.New("short url not found")
	errShortURLCodeConflict = errors.New("short url code conflict")
	newCodeRandSourceFn     = func(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }
	codeAllowedPattern      = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

type shortURL struct {
	ID        int64
	Code      string
	TargetURL string
	CreatedAt time.Time
}

type shortURLResponse struct {
	Code      string `json:"code"`
	TargetURL string `json:"target_url"`
	ShortPath string `json:"short_path"`
	CreatedAt string `json:"created_at"`
}

type createShortURLInput struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

type shortURLRepository interface {
	CreateShortURL(context.Context, createShortURLRepositoryInput, time.Time) (shortURL, error)
	GetShortURLByCode(context.Context, string) (shortURL, error)
}

type createShortURLRepositoryInput struct {
	Code      string
	TargetURL string
}

type memoryShortURLRepository struct {
	mu     sync.Mutex
	nextID int64
	byCode map[string]shortURL
}

type shortURLAPI struct {
	repo shortURLRepository
}

func newShortURLAPI(repo shortURLRepository) *shortURLAPI {
	return &shortURLAPI{repo: repo}
}

func newMemoryShortURLRepository() *memoryShortURLRepository {
	return &memoryShortURLRepository{
		nextID: 1,
		byCode: make(map[string]shortURL),
	}
}

func (a *shortURLAPI) createShortURL(w http.ResponseWriter, r *http.Request) {
	var in createShortURLInput
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "invalid_json",
			Message: "invalid JSON request body",
		})
		return
	}

	in.URL = strings.TrimSpace(in.URL)
	in.Code = strings.TrimSpace(in.Code)
	if fields := validateCreateShortURLInput(in); len(fields) > 0 {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "validation_error",
			Message: "request validation failed",
			Fields:  fields,
		})
		return
	}

	code := in.Code
	if code == "" {
		code = generateShortCode(nowFn().UTC().UnixNano(), shortURLCodeLength)
	}

	created, err := a.repo.CreateShortURL(r.Context(), createShortURLRepositoryInput{
		Code:      code,
		TargetURL: in.URL,
	}, nowFn().UTC())
	if err != nil {
		respondShortURLRepositoryError(w, r, err)
		return
	}

	respondJSON(w, r, http.StatusCreated, map[string]any{"short_url": toShortURLResponse(created)})
}

func (a *shortURLAPI) getShortURL(w http.ResponseWriter, r *http.Request) {
	code, ok := parseShortCodePathValue(w, r)
	if !ok {
		return
	}

	item, err := a.repo.GetShortURLByCode(r.Context(), code)
	if err != nil {
		respondShortURLRepositoryError(w, r, err)
		return
	}

	respondJSON(w, r, http.StatusOK, map[string]any{"short_url": toShortURLResponse(item)})
}

func (a *shortURLAPI) redirectByCode(w http.ResponseWriter, r *http.Request) {
	code, ok := parseShortCodePathValue(w, r)
	if !ok {
		return
	}

	item, err := a.repo.GetShortURLByCode(r.Context(), code)
	if err != nil {
		respondShortURLRepositoryError(w, r, err)
		return
	}

	http.Redirect(w, r, item.TargetURL, http.StatusFound)
}

func validateCreateShortURLInput(in createShortURLInput) map[string]string {
	fields := map[string]string{}
	if in.URL == "" {
		fields["url"] = "url is required"
	} else {
		u, err := url.ParseRequestURI(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fields["url"] = "url must be a valid absolute http or https URL"
		}
	}

	if in.Code != "" {
		if len(in.Code) < 4 || len(in.Code) > 32 {
			fields["code"] = "code length must be between 4 and 32"
		} else if !codeAllowedPattern.MatchString(in.Code) {
			fields["code"] = "code must contain only letters, digits, hyphen, underscore"
		}
	}

	if len(fields) == 0 {
		return nil
	}
	return fields
}

func parseShortCodePathValue(w http.ResponseWriter, r *http.Request) (string, bool) {
	code := strings.TrimSpace(r.PathValue("code"))
	if code == "" {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "invalid_code",
			Message: "code is required",
		})
		return "", false
	}
	if !codeAllowedPattern.MatchString(code) {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "invalid_code",
			Message: "code must contain only letters, digits, hyphen, underscore",
		})
		return "", false
	}
	return code, true
}

func respondShortURLRepositoryError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errShortURLNotFound) {
		respondErrorJSON(w, r, http.StatusNotFound, apiError{
			Code:    "not_found",
			Message: "short url not found",
		})
		return
	}
	if errors.Is(err, errShortURLCodeConflict) {
		respondErrorJSON(w, r, http.StatusConflict, apiError{
			Code:    "code_conflict",
			Message: "short code already exists",
		})
		return
	}
	loggerPrintfFn("short url repository error path=%s err=%v", r.URL.Path, err)
	respondInternalServerError(w, r)
}

func toShortURLResponse(s shortURL) shortURLResponse {
	return shortURLResponse{
		Code:      s.Code,
		TargetURL: s.TargetURL,
		ShortPath: "/" + s.Code,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func generateShortCode(seed int64, length int) string {
	r := newCodeRandSourceFn(seed)
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = shortURLAlphabet[r.Intn(len(shortURLAlphabet))]
	}
	return string(buf)
}

func (s *memoryShortURLRepository) CreateShortURL(_ context.Context, in createShortURLRepositoryInput, ts time.Time) (shortURL, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byCode[in.Code]; exists {
		return shortURL{}, errShortURLCodeConflict
	}

	item := shortURL{
		ID:        s.nextID,
		Code:      in.Code,
		TargetURL: in.TargetURL,
		CreatedAt: ts,
	}
	s.byCode[item.Code] = item
	s.nextID++
	return item, nil
}

func (s *memoryShortURLRepository) GetShortURLByCode(_ context.Context, code string) (shortURL, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.byCode[code]
	if !ok {
		return shortURL{}, errShortURLNotFound
	}
	return item, nil
}
