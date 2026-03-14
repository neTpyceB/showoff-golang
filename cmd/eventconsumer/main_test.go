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

func TestRunConsumerEnvValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_ADDR", "")
	if err := runConsumer(); err == nil {
		t.Fatal("expected db url error")
	}
	t.Setenv("DATABASE_URL", "postgres://bad")
	if err := runConsumer(); err == nil {
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
	oldRun := runConsumerFn
	oldFatal := fatalf
	defer func() {
		runConsumerFn = oldRun
		fatalf = oldFatal
	}()

	called := false
	runConsumerFn = func() error { return errors.New("boom") }
	fatalf = func(string, ...any) { called = true }
	main()
	if !called {
		t.Fatal("expected fatal call")
	}

	called = false
	runConsumerFn = func() error { return context.Canceled }
	main()
	if called {
		t.Fatal("unexpected fatal on canceled")
	}
}

func TestRunConsumerWithRealDepsAndCanceledContext(t *testing.T) {
	oldOpen := sqlOpenFn
	oldPing := dbPingFn
	oldNewConsumer := newConsumerFn
	oldSignal := signalNotifyContext
	oldTicker := tickerDuration
	t.Cleanup(func() {
		sqlOpenFn = oldOpen
		dbPingFn = oldPing
		newConsumerFn = oldNewConsumer
		signalNotifyContext = oldSignal
		tickerDuration = oldTicker
	})
	sqlOpenFn = func(string, string) (*sql.DB, error) {
		db, _, err := sqlmock.New()
		return db, err
	}
	dbPingFn = func(*sql.DB) error { return nil }
	newConsumerFn = func(eventpipe.BrokerConsumer, eventpipe.DLQRepository, eventpipe.EventHandler, eventpipe.ConsumerConfig) *eventpipe.Consumer {
		return eventpipe.NewConsumer(&stubBroker{}, &stubDLQ{}, &stubHandler{}, eventpipe.ConsumerConfig{
			Stream:       "s",
			Group:        "g",
			ConsumerName: "c",
			ReadCount:    1,
			BlockTimeout: time.Millisecond,
			MaxAttempts:  1,
			LoggerPrintf: func(string, ...any) {},
		})
	}
	signalNotifyContext = func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, func() {}
	}
	tickerDuration = time.Millisecond

	t.Setenv("DATABASE_URL", "postgres://showoff:showoff@db:5432/showoff?sslmode=disable")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("EVENT_STREAM", "orders.events")
	t.Setenv("EVENT_CONSUMER_GROUP", "orders-consumers")
	t.Setenv("EVENT_CONSUMER_NAME", "consumer-test")
	err := runConsumer()
	if err == nil {
		t.Fatal("expected canceled context error")
	}
}

func TestRunConsumerOpenPingAndGroupError(t *testing.T) {
	oldOpen := sqlOpenFn
	oldPing := dbPingFn
	t.Cleanup(func() {
		sqlOpenFn = oldOpen
		dbPingFn = oldPing
	})

	t.Setenv("DATABASE_URL", "x")
	t.Setenv("REDIS_ADDR", "x")
	sqlOpenFn = func(string, string) (*sql.DB, error) { return nil, errors.New("open fail") }
	if err := runConsumer(); err == nil {
		t.Fatal("expected open error")
	}

	sqlOpenFn = func(string, string) (*sql.DB, error) {
		db, _, err := sqlmock.New()
		return db, err
	}
	dbPingFn = func(*sql.DB) error { return errors.New("ping fail") }
	if err := runConsumer(); err == nil {
		t.Fatal("expected ping error")
	}

	// ensure group/read path error with real broker and mocked DB.
	dbPingFn = func(*sql.DB) error { return nil }
	t.Setenv("REDIS_ADDR", "127.0.0.1:0")
	if err := runConsumer(); err == nil {
		t.Fatal("expected broker/ensure-group error")
	}
}

func TestRunConsumerRunOnceErrorAndTickerBranch(t *testing.T) {
	oldOpen := sqlOpenFn
	oldPing := dbPingFn
	oldNewConsumer := newConsumerFn
	oldSignal := signalNotifyContext
	oldTicker := tickerDuration
	t.Cleanup(func() {
		sqlOpenFn = oldOpen
		dbPingFn = oldPing
		newConsumerFn = oldNewConsumer
		signalNotifyContext = oldSignal
		tickerDuration = oldTicker
	})

	sqlOpenFn = func(string, string) (*sql.DB, error) {
		db, _, err := sqlmock.New()
		return db, err
	}
	dbPingFn = func(*sql.DB) error { return nil }
	t.Setenv("DATABASE_URL", "x")
	t.Setenv("REDIS_ADDR", "redis:6379")

	// Force RunOnce error branch.
	newConsumerFn = func(eventpipe.BrokerConsumer, eventpipe.DLQRepository, eventpipe.EventHandler, eventpipe.ConsumerConfig) *eventpipe.Consumer {
		return eventpipe.NewConsumer(&stubBrokerErr{}, &stubDLQ{}, &stubHandler{}, eventpipe.ConsumerConfig{
			Stream:       "s",
			Group:        "g",
			ConsumerName: "c",
			ReadCount:    1,
			BlockTimeout: time.Millisecond,
			MaxAttempts:  1,
			LoggerPrintf: func(string, ...any) {},
		})
	}
	if err := runConsumer(); err == nil {
		t.Fatal("expected run once error")
	}

	// Force ticker select branch at least once.
	newConsumerFn = func(eventpipe.BrokerConsumer, eventpipe.DLQRepository, eventpipe.EventHandler, eventpipe.ConsumerConfig) *eventpipe.Consumer {
		return eventpipe.NewConsumer(&stubBroker{}, &stubDLQ{}, &stubHandler{}, eventpipe.ConsumerConfig{
			Stream:       "s",
			Group:        "g",
			ConsumerName: "c",
			ReadCount:    1,
			BlockTimeout: time.Millisecond,
			MaxAttempts:  1,
			LoggerPrintf: func(string, ...any) {},
		})
	}
	tickerDuration = time.Millisecond
	signalNotifyContext = func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(3 * time.Millisecond)
			cancel()
		}()
		return ctx, func() {}
	}
	if err := runConsumer(); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
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

type stubBroker struct{}

func (stubBroker) Read(context.Context, string, string, string, int, time.Duration) ([]eventpipe.BrokerConsumedMessage, error) {
	return nil, nil
}
func (stubBroker) Ack(context.Context, string, string, ...string) error { return nil }

type stubDLQ struct{}

func (stubDLQ) InsertDLQ(context.Context, string, string, []byte, string, int, string, string, time.Time) error {
	return nil
}

type stubHandler struct{}

func (stubHandler) Handle(context.Context, []byte) error { return nil }

type stubBrokerErr struct{}

func (stubBrokerErr) Read(context.Context, string, string, string, int, time.Duration) ([]eventpipe.BrokerConsumedMessage, error) {
	return nil, errors.New("read fail")
}
func (stubBrokerErr) Ack(context.Context, string, string, ...string) error { return nil }
