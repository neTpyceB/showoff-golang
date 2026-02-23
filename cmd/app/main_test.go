package main

import (
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

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		wantBody := "Hello from Go (running in Docker)!\n"
		if rec.Body.String() != wantBody {
			t.Fatalf("body = %q, want %q", rec.Body.String(), wantBody)
		}

		return nil
	}

	if err := run(); err != nil {
		t.Fatalf("run() error = %v", err)
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

	t.Cleanup(func() {
		listenAndServe = oldListenAndServe
		fatalf = oldFatalf
	})
}
