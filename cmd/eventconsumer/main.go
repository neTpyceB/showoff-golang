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
var runConsumerFn = runConsumer
var sqlOpenFn = sql.Open
var dbPingFn = pingDB
var newRedisBrokerFn = eventpipe.NewRedisBroker
var newConsumerFn = eventpipe.NewConsumer
var newProjectionHandlerFn = eventpipe.NewProjectionHandler
var tickerDuration = 200 * time.Millisecond

func pingDB(db *sql.DB) error {
	return db.PingContext(context.Background())
}

func runConsumer() error {
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

	stream := getenv("EVENT_STREAM", "orders.events")
	group := getenv("EVENT_CONSUMER_GROUP", "orders-consumers")
	consumer := getenv("EVENT_CONSUMER_NAME", "consumer-1")
	if err := broker.EnsureConsumerGroup(context.Background(), stream, group); err != nil {
		return err
	}

	c := newConsumerFn(broker, repo, newProjectionHandlerFn(repo), eventpipe.ConsumerConfig{
		Stream:       stream,
		Group:        group,
		ConsumerName: consumer,
		ReadCount:    100,
		BlockTimeout: 2 * time.Second,
		MaxAttempts:  6,
		ServiceName:  "event-consumer",
	})

	ctx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(tickerDuration)
	defer ticker.Stop()
	for {
		if _, err := c.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if err := runConsumerFn(); err != nil && !errors.Is(err, context.Canceled) {
		fatalf("event consumer error: %v", err)
	}
}
