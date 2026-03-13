package httpapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShortURLAPIFlow(t *testing.T) {
	restoreGlobals(t)

	nowFn = func() time.Time {
		return time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	}
	newRequestIDFn = func() string { return "req-short-1" }

	h := NewHandler()

	t.Run("create with explicit code", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/short-urls", `{"url":"https://example.com/page","code":"go1234"}`)
		assertStatus(t, rec, http.StatusCreated)

		var body struct {
			Data struct {
				ShortURL shortURLResponse `json:"short_url"`
			} `json:"data"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		if body.Data.ShortURL.Code != "go1234" {
			t.Fatalf("code = %q", body.Data.ShortURL.Code)
		}
		if body.Data.ShortURL.TargetURL != "https://example.com/page" {
			t.Fatalf("target url = %q", body.Data.ShortURL.TargetURL)
		}
		if body.Data.ShortURL.ShortPath != "/go1234" {
			t.Fatalf("short path = %q", body.Data.ShortURL.ShortPath)
		}
		if body.Data.ShortURL.CreatedAt != "2026-03-13T10:00:00Z" {
			t.Fatalf("created_at = %q", body.Data.ShortURL.CreatedAt)
		}
	})

	t.Run("get metadata", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/short-urls/go1234", "")
		assertStatus(t, rec, http.StatusOK)
		var body struct {
			Data struct {
				ShortURL shortURLResponse `json:"short_url"`
			} `json:"data"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		if body.Data.ShortURL.Code != "go1234" {
			t.Fatalf("code = %q", body.Data.ShortURL.Code)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/go1234", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "https://example.com/page" {
			t.Fatalf("location = %q", loc)
		}
	})

	t.Run("create auto code", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/short-urls", `{"url":"https://golang.org/"}`)
		assertStatus(t, rec, http.StatusCreated)
		var body struct {
			Data struct {
				ShortURL shortURLResponse `json:"short_url"`
			} `json:"data"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		if body.Data.ShortURL.Code == "" {
			t.Fatal("expected generated code")
		}
		if len(body.Data.ShortURL.Code) != shortURLCodeLength {
			t.Fatalf("generated length = %d", len(body.Data.ShortURL.Code))
		}
	})

	t.Run("validation and errors", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/short-urls", `{"url":"ftp://example.com"}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "validation_error")

		rec = doRequest(t, h, http.MethodPost, "/short-urls", `{"url":"https://example.com","code":"ab"}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "validation_error")

		rec = doRequest(t, h, http.MethodPost, "/short-urls", `{"url":"https://example.com","code":"bad code"}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "validation_error")

		rec = doRequest(t, h, http.MethodPost, "/short-urls", `{"url":"https://example.com","code":"go1234"}`)
		assertStatus(t, rec, http.StatusConflict)
		assertAPIErrorCode(t, rec, "code_conflict")

		rec = doRequest(t, h, http.MethodGet, "/short-urls/not_found", "")
		assertStatus(t, rec, http.StatusNotFound)
		assertAPIErrorCode(t, rec, "not_found")

		rec = doRequest(t, h, http.MethodGet, "/bad%20code", "")
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "invalid_code")
	})
}

type stubShortURLRepo struct {
	createErr error
	getErr    error
}

func (s stubShortURLRepo) CreateShortURL(context.Context, createShortURLRepositoryInput, time.Time) (shortURL, error) {
	if s.createErr != nil {
		return shortURL{}, s.createErr
	}
	return shortURL{Code: "abc12345", TargetURL: "https://example.com", CreatedAt: time.Now()}, nil
}

func (s stubShortURLRepo) GetShortURLByCode(context.Context, string) (shortURL, error) {
	if s.getErr != nil {
		return shortURL{}, s.getErr
	}
	return shortURL{Code: "abc12345", TargetURL: "https://example.com", CreatedAt: time.Now()}, nil
}

func TestShortURLRepositoryErrorBranches(t *testing.T) {
	restoreGlobals(t)
	var logs []string
	loggerPrintfFn = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	h := NewHandlerWithRepositories(newMemoryTaskRepository(), stubShortURLRepo{createErr: errors.New("boom")})
	rec := doRequest(t, h, http.MethodPost, "/short-urls", `{"url":"https://example.com"}`)
	assertStatus(t, rec, http.StatusInternalServerError)
	assertAPIErrorCode(t, rec, "internal_error")
	if len(logs) == 0 || !strings.Contains(logs[0], "short url repository error") {
		t.Fatalf("logs = %#v", logs)
	}

	h = NewHandlerWithRepositories(newMemoryTaskRepository(), stubShortURLRepo{getErr: errors.New("boom")})
	rec = doRequest(t, h, http.MethodGet, "/short-urls/abc12345", "")
	assertStatus(t, rec, http.StatusInternalServerError)
	assertAPIErrorCode(t, rec, "internal_error")
}

func TestShortURLHelpers(t *testing.T) {
	if got := generateShortCode(7, shortURLCodeLength); len(got) != shortURLCodeLength {
		t.Fatalf("generated length = %d", len(got))
	}
	for _, ch := range generateShortCode(8, shortURLCodeLength) {
		if !strings.ContainsRune(shortURLAlphabet, ch) {
			t.Fatalf("invalid char %q", ch)
		}
	}

	repo := newMemoryShortURLRepository()
	_, err := repo.CreateShortURL(context.Background(), createShortURLRepositoryInput{Code: "abcd", TargetURL: "https://example.com"}, time.Now())
	if err != nil {
		t.Fatalf("CreateShortURL err = %v", err)
	}
	if _, err := repo.CreateShortURL(context.Background(), createShortURLRepositoryInput{Code: "abcd", TargetURL: "https://example.com"}, time.Now()); !errors.Is(err, errShortURLCodeConflict) {
		t.Fatalf("duplicate err = %v", err)
	}
	if _, err := repo.GetShortURLByCode(context.Background(), "missing"); !errors.Is(err, errShortURLNotFound) {
		t.Fatalf("not found err = %v", err)
	}
}

func TestShortURLInputAndPathParsingBranches(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.SetPathValue("code", "")
	req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey, "req-1"))
	rec := httptest.NewRecorder()
	if _, ok := parseShortCodePathValue(rec, req); ok {
		t.Fatal("expected parseShortCodePathValue to fail on empty code")
	}
	assertStatus(t, rec, http.StatusBadRequest)

	fields := validateCreateShortURLInput(createShortURLInput{})
	if fields["url"] == "" {
		t.Fatalf("expected url validation error, got %#v", fields)
	}
}

func TestShortURLInvalidJSONAndInvalidMetadataCode(t *testing.T) {
	h := NewHandler()

	rec := doRequest(t, h, http.MethodPost, "/short-urls", `{"url":`)
	assertStatus(t, rec, http.StatusBadRequest)
	assertAPIErrorCode(t, rec, "invalid_json")

	rec = doRequest(t, h, http.MethodGet, "/short-urls/bad%20code", "")
	assertStatus(t, rec, http.StatusBadRequest)
	assertAPIErrorCode(t, rec, "invalid_code")
}
