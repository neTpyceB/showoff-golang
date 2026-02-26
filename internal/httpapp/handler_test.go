package httpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewHandlerHelloAndHealthRoutes(t *testing.T) {
	restoreGlobals(t)

	nowFn = func() time.Time {
		return time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)
	}

	var reqCounter int
	newRequestIDFn = func() string {
		reqCounter++
		return fmt.Sprintf("req-test-%d", reqCounter)
	}

	var logs []string
	loggerPrintfFn = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	handler := NewHandler()

	t.Run("hello", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/hello", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Fatalf("content-type = %q", got)
		}
		if got := rec.Header().Get("X-Request-ID"); got != "req-test-1" {
			t.Fatalf("x-request-id = %q", got)
		}

		var got struct {
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
			Meta struct {
				RequestID string `json:"request_id"`
			} `json:"meta"`
		}
		decodeJSON(t, rec.Body.Bytes(), &got)

		if got.Data.Message != "Hello from Go (running in Docker)!" {
			t.Fatalf("message = %q", got.Data.Message)
		}
		if got.Meta.RequestID != "req-test-1" {
			t.Fatalf("body request_id = %q", got.Meta.RequestID)
		}
	})

	t.Run("health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("X-Request-ID"); got != "req-test-2" {
			t.Fatalf("x-request-id = %q", got)
		}

		var got struct {
			Data struct {
				Status    string `json:"status"`
				Service   string `json:"service"`
				Timestamp string `json:"timestamp"`
			} `json:"data"`
			Meta struct {
				RequestID string `json:"request_id"`
			} `json:"meta"`
		}
		decodeJSON(t, rec.Body.Bytes(), &got)

		if got.Data.Status != "ok" || got.Data.Service != serviceName {
			t.Fatalf("health data = %+v", got.Data)
		}
		if got.Data.Timestamp != "2026-02-26T12:00:00Z" {
			t.Fatalf("timestamp = %q", got.Data.Timestamp)
		}
		if got.Meta.RequestID != "req-test-2" {
			t.Fatalf("body request_id = %q", got.Meta.RequestID)
		}
	})

	t.Run("not found still gets middleware", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/missing", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		if rec.Header().Get("X-Request-ID") == "" {
			t.Fatal("expected x-request-id header")
		}
	})

	allLogs := strings.Join(logs, "\n")
	if !strings.Contains(allLogs, "http_request method=GET path=/hello status=200") {
		t.Fatalf("missing hello log: %s", allLogs)
	}
	if !strings.Contains(allLogs, "http_request method=GET path=/health status=200") {
		t.Fatalf("missing health log: %s", allLogs)
	}
	if !strings.Contains(allLogs, "path=/missing status=404") {
		t.Fatalf("missing 404 log: %s", allLogs)
	}
}

func TestRespondJSONEncodeErrorIsLogged(t *testing.T) {
	restoreGlobals(t)

	var logs []string
	loggerPrintfFn = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	jsonEncodeFn = func(http.ResponseWriter, any) error {
		return errors.New("encode failed")
	}

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey, "req-1"))
	rec := httptest.NewRecorder()

	respondJSON(rec, req, http.StatusCreated, helloResponse{Message: "x"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", got)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "response encode error") {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestRequestIDFromContextAndRecorderDefaults(t *testing.T) {
	if _, ok := requestIDFromContext(context.Background()); ok {
		t.Fatal("expected no request id")
	}

	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if rec.StatusCode() != http.StatusOK {
		t.Fatalf("default status = %d", rec.StatusCode())
	}
}

func TestWithRequestLoggingUsesDefaultStatusWhenHandlerWritesBodyOnly(t *testing.T) {
	restoreGlobals(t)

	nowFn = func() time.Time { return time.Unix(0, 0) }

	var logs []string
	loggerPrintfFn = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	handler := withRequestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/body-only", nil)
	req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey, "req-xyz"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "status=200") {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestWithRequestIDMiddlewareSetsHeaderAndContext(t *testing.T) {
	restoreGlobals(t)

	newRequestIDFn = func() string { return "req-abc" }

	var gotContextID string
	handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContextID, _ = requestIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "req-abc" {
		t.Fatalf("header request id = %q", got)
	}
	if gotContextID != "req-abc" {
		t.Fatalf("context request id = %q", gotContextID)
	}
}

func decodeJSON(t *testing.T, data []byte, out any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(out); err != nil {
		t.Fatalf("decode json: %v\n%s", err, data)
	}
}

func restoreGlobals(t *testing.T) {
	t.Helper()

	oldNow := nowFn
	oldLogger := loggerPrintfFn
	oldReqID := newRequestIDFn
	oldJSONEncode := jsonEncodeFn
	oldSeq := requestSeq

	t.Cleanup(func() {
		nowFn = oldNow
		loggerPrintfFn = oldLogger
		newRequestIDFn = oldReqID
		jsonEncodeFn = oldJSONEncode
		requestSeq = oldSeq
	})
}
