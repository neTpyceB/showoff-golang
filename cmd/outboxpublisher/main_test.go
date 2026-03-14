package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"showoff-golang/internal/eventpipe"
)

func TestRunPublisherEnvValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_ADDR", "")
	if err := runPublisher(); err == nil {
		t.Fatal("expected db url error")
	}
	t.Setenv("DATABASE_URL", "postgres://bad")
	if err := runPublisher(); err == nil {
		t.Fatal("expected redis addr error")
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("X_TEST", "")
	if got := getenv("X_TEST", "fallback"); got != "fallback" {
		t.Fatalf("got=%q", got)
	}
	t.Setenv("X_TEST", "v")
	if got := getenv("X_TEST", "fallback"); got != "v" {
		t.Fatalf("got=%q", got)
	}
}

func TestMainFatalPath(t *testing.T) {
	oldRun := runPublisherFn
	oldFatal := fatalf
	defer func() {
		runPublisherFn = oldRun
		fatalf = oldFatal
	}()

	called := false
	runPublisherFn = func() error { return errors.New("boom") }
	fatalf = func(string, ...any) { called = true }
	main()
	if !called {
		t.Fatal("expected fatal call")
	}

	called = false
	runPublisherFn = func() error { return context.Canceled }
	main()
	if called {
		t.Fatal("unexpected fatal on canceled")
	}
	_ = os.Getenv
}

func TestRunPublisherWithRealDepsAndCanceledContext(t *testing.T) {
	oldOpen := sqlOpenFn
	oldPing := dbPingFn
	oldNewPublisher := newPublisherFn
	oldSignal := signalNotifyContext
	t.Cleanup(func() {
		sqlOpenFn = oldOpen
		dbPingFn = oldPing
		newPublisherFn = oldNewPublisher
		signalNotifyContext = oldSignal
	})
	sqlOpenFn = func(string, string) (*sql.DB, error) {
		db, _, err := sqlmock.New()
		return db, err
	}
	dbPingFn = func(*sql.DB) error { return nil }
	newPublisherFn = func(eventpipe.OutboxRepository, eventpipe.BrokerPublisher, eventpipe.PublisherConfig) *eventpipe.Publisher {
		return eventpipe.NewPublisher(&fakeRepo{}, &fakeBroker{}, eventpipe.PublisherConfig{
			DisableTicker: true,
			LoggerPrintf:  func(string, ...any) {},
		})
	}
	signalNotifyContext = func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, func() {}
	}

	t.Setenv("DATABASE_URL", "postgres://showoff:showoff@db:5432/showoff?sslmode=disable")
	t.Setenv("REDIS_ADDR", "redis:6379")
	err := runPublisher()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestRunPublisherOpenPingErrors(t *testing.T) {
	oldOpen := sqlOpenFn
	oldPing := dbPingFn
	t.Cleanup(func() {
		sqlOpenFn = oldOpen
		dbPingFn = oldPing
	})

	t.Setenv("DATABASE_URL", "x")
	t.Setenv("REDIS_ADDR", "x")
	sqlOpenFn = func(string, string) (*sql.DB, error) { return nil, errors.New("open fail") }
	if err := runPublisher(); err == nil {
		t.Fatal("expected open error")
	}

	sqlOpenFn = func(string, string) (*sql.DB, error) {
		db, _, err := sqlmock.New()
		return db, err
	}
	dbPingFn = func(*sql.DB) error { return errors.New("ping fail") }
	if err := runPublisher(); err == nil {
		t.Fatal("expected ping error")
	}
}

func TestPingDB(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	if err := pingDB(db); err != nil {
		t.Fatalf("ping err: %v", err)
	}
	_ = db.Close()
	if err := pingDB(db); err == nil {
		t.Fatal("expected ping error on closed db")
	}
}

type fakeRepo struct{}

func (fakeRepo) FetchPending(context.Context, int, time.Time) ([]eventpipe.OutboxEvent, error) {
	return nil, nil
}
func (fakeRepo) MarkPublished(context.Context, int64, time.Time) error          { return nil }
func (fakeRepo) MarkRetry(context.Context, int64, int, time.Time, string) error { return nil }
func (fakeRepo) MarkDead(context.Context, int64, int, string) error             { return nil }

type fakeBroker struct{}

func (fakeBroker) Publish(context.Context, string, eventpipe.BrokerMessage) error { return nil }
