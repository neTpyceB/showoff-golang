package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRunReturnsListenError(t *testing.T) {
	restoreGlobals(t)

	serverListenAndServe = func(s *http.Server) error {
		if s.Addr != defaultAddr {
			t.Fatalf("addr = %q, want %q", s.Addr, defaultAddr)
		}
		if s.Handler == nil {
			t.Fatal("expected handler")
		}
		return errors.New("listen failed")
	}

	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}

	if err := run(); err == nil || !strings.Contains(err.Error(), "listen failed") {
		t.Fatalf("run() err = %v", err)
	}
}

func TestRunReturnsNilWhenServerStopsWithoutError(t *testing.T) {
	restoreGlobals(t)

	serverListenAndServe = func(_ *http.Server) error { return nil }
	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}

	if err := run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunGracefulShutdownOnSignal(t *testing.T) {
	restoreGlobals(t)
	t.Setenv("DATABASE_URL", "postgres://example")

	var (
		closeCalled    bool
		shutdownCalled bool
		factoryCalled  bool
		signalCancelFn context.CancelFunc
	)

	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		cctx, cancel := context.WithCancel(ctx)
		signalCancelFn = cancel
		return cctx, cancel
	}

	newPostgresHandler = func(_ context.Context, databaseURL string) (http.Handler, func() error, error) {
		factoryCalled = true
		if databaseURL != "postgres://example" {
			t.Fatalf("databaseURL = %q", databaseURL)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}), func() error {
				closeCalled = true
				return nil
			}, nil
	}

	serverListenAndServe = func(_ *http.Server) error {
		if signalCancelFn == nil {
			t.Fatal("signal cancel function not set")
		}
		signalCancelFn()
		return http.ErrServerClosed
	}

	serverShutdown = func(_ *http.Server, _ context.Context) error {
		shutdownCalled = true
		return nil
	}

	if err := run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !factoryCalled {
		t.Fatal("expected postgres handler factory call")
	}
	if !shutdownCalled {
		t.Fatal("expected shutdown to be called")
	}
	if !closeCalled {
		t.Fatal("expected closeFn to be called")
	}
}

func TestRunReturnsShutdownError(t *testing.T) {
	restoreGlobals(t)

	var signalCancelFn context.CancelFunc
	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		cctx, cancel := context.WithCancel(ctx)
		signalCancelFn = cancel
		return cctx, cancel
	}

	serverListenAndServe = func(_ *http.Server) error {
		signalCancelFn()
		return http.ErrServerClosed
	}
	serverShutdown = func(_ *http.Server, _ context.Context) error {
		return errors.New("shutdown failed")
	}

	if err := run(); err == nil || !strings.Contains(err.Error(), "shutdown failed") {
		t.Fatalf("run() err = %v", err)
	}
}

func TestRunReturnsPostShutdownServeError(t *testing.T) {
	restoreGlobals(t)

	var signalCancelFn context.CancelFunc
	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		cctx, cancel := context.WithCancel(ctx)
		signalCancelFn = cancel
		return cctx, cancel
	}

	serverListenAndServe = func(_ *http.Server) error {
		signalCancelFn()
		return errors.New("serve failed after signal")
	}
	serverShutdown = func(_ *http.Server, _ context.Context) error { return nil }

	if err := run(); err == nil || !strings.Contains(err.Error(), "serve failed after signal") {
		t.Fatalf("run() err = %v", err)
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

func TestRunMetricsRoutesWithRedisStore(t *testing.T) {
	restoreGlobals(t)
	mr := miniredis.RunT(t)
	t.Setenv("METRICS_REDIS_ADDR", mr.Addr())

	serverListenAndServe = func(s *http.Server) error {
		postRec := httptest.NewRecorder()
		postReq := httptest.NewRequest(http.MethodPost, "/metrics/events", bytes.NewBufferString(`{"source":"http","name":"GET:/x","status":"200","duration_ms":3}`))
		s.Handler.ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusAccepted {
			t.Fatalf("post status = %d", postRec.Code)
		}

		getRec := httptest.NewRecorder()
		getReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		s.Handler.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("get status = %d", getRec.Code)
		}
		return nil
	}
	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}

	if err := run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestMainCallsFatalfWhenRunFails(t *testing.T) {
	restoreGlobals(t)

	serverListenAndServe = func(_ *http.Server) error {
		return errors.New("boom")
	}
	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
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

	signalNotifyContext = func(ctx context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		return cctx, func() {}
	}
	serverListenAndServe = func(_ *http.Server) error {
		return http.ErrServerClosed
	}
	serverShutdown = func(_ *http.Server, _ context.Context) error { return nil }

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

	oldFatalf := fatalf
	oldNewPostgresHandler := newPostgresHandler
	oldSignalNotifyContext := signalNotifyContext
	oldListen := serverListenAndServe
	oldShutdown := serverShutdown

	t.Cleanup(func() {
		fatalf = oldFatalf
		newPostgresHandler = oldNewPostgresHandler
		signalNotifyContext = oldSignalNotifyContext
		serverListenAndServe = oldListen
		serverShutdown = oldShutdown
	})
}
