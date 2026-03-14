package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"showoff-golang/internal/eventpipe"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var fatalf = log.Fatalf
var signalNotifyContext = signal.NotifyContext
var runPublisherFn = runPublisher
var sqlOpenFn = sql.Open
var dbPingFn = pingDB
var newRedisBrokerFn = eventpipe.NewRedisBroker
var newPublisherFn = eventpipe.NewPublisher

func pingDB(db *sql.DB) error {
	return db.PingContext(context.Background())
}

func runPublisher() error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		return errors.New("REDIS_ADDR is required")
	}

	db, err := sqlOpenFn("pgx", dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := dbPingFn(db); err != nil {
		return err
	}

	repo := eventpipe.NewPostgresRepo(db)
	broker := newRedisBrokerFn(redisAddr, os.Getenv("REDIS_PASSWORD"), 0)
	defer broker.Close()

	publisher := newPublisherFn(repo, broker, eventpipe.PublisherConfig{
		Topic:        getenv("EVENT_STREAM", "orders.events"),
		BatchSize:    200,
		MaxAttempts:  8,
		BaseBackoff:  500 * time.Millisecond,
		PollInterval: 500 * time.Millisecond,
		ServiceName:  "outbox-publisher",
	})

	ctx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return publisher.Run(ctx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if err := runPublisherFn(); err != nil && !errors.Is(err, context.Canceled) {
		fatalf("outbox publisher error: %v", err)
	}
}
