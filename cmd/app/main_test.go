package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunStartsServerWithExpectedAddressAndHandler(t *testing.T) {
	restoreGlobals(t)

	listenAndServe = func(addr string, handler http.Handler) error {
		if addr != defaultAddr {
			t.Fatalf("addr = %q, want %q", addr, defaultAddr)
		}

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Fatalf("content-type = %q", got)
		}

		var payload struct {
			Data struct {
				Status string `json:"status"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode json: %v", err)
		}
		if payload.Data.Status != "ok" {
			t.Fatalf("health status = %q", payload.Data.Status)
		}

		return nil
	}

	if err := run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunUsesPostgresHandlerWhenDatabaseURLSet(t *testing.T) {
	restoreGlobals(t)
	t.Setenv("DATABASE_URL", "postgres://example")

	var (
		gotURL        string
		closeCalled   bool
		factoryCalled bool
		handlerCalled bool
	)

	newPostgresHandler = func(_ context.Context, databaseURL string) (http.Handler, func() error, error) {
		factoryCalled = true
		gotURL = databaseURL
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusNoContent)
			}), func() error {
				closeCalled = true
				return nil
			}, nil
	}

	listenAndServe = func(addr string, handler http.Handler) error {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d", rec.Code)
		}
		return nil
	}

	if err := run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !factoryCalled || gotURL != "postgres://example" {
		t.Fatalf("factoryCalled=%v gotURL=%q", factoryCalled, gotURL)
	}
	if !handlerCalled {
		t.Fatal("expected postgres-backed handler to run")
	}
	if !closeCalled {
		t.Fatal("expected closeFn to be called")
	}
}

func TestRunReturnsPostgresHandlerFactoryError(t *testing.T) {
	restoreGlobals(t)
	t.Setenv("DATABASE_URL", "postgres://example")

	newPostgresHandler = func(context.Context, string) (http.Handler, func() error, error) {
		return nil, nil, errors.New("db init failed")
	}

	if err := run(); err == nil || !strings.Contains(err.Error(), "db init failed") {
		t.Fatalf("run() err = %v", err)
	}
}

func TestMainCallsFatalfWhenRunFails(t *testing.T) {
	restoreGlobals(t)

	boom := errors.New("boom")
	listenAndServe = func(string, http.Handler) error {
		return boom
	}

	var (
		called bool
		msg    string
	)
	fatalf = func(format string, args ...any) {
		called = true
		msg = fmt.Sprintf(format, args...)
	}

	main()

	if !called {
		t.Fatal("expected fatalf to be called")
	}

	if !strings.Contains(msg, "server error: boom") {
		t.Fatalf("fatal message = %q", msg)
	}
}

func TestMainDoesNotCallFatalfWhenRunSucceeds(t *testing.T) {
	restoreGlobals(t)

	listenAndServe = func(string, http.Handler) error {
		return nil
	}

	called := false
	fatalf = func(string, ...any) {
		called = true
	}

	main()

	if called {
		t.Fatal("fatalf should not be called on successful run")
	}
}

func restoreGlobals(t *testing.T) {
	t.Helper()

	oldListenAndServe := listenAndServe
	oldFatalf := fatalf
	oldNewPostgresHandler := newPostgresHandler

	t.Cleanup(func() {
		listenAndServe = oldListenAndServe
		fatalf = oldFatalf
		newPostgresHandler = oldNewPostgresHandler
	})
}
