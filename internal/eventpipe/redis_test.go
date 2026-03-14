package eventpipe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisBrokerIntegration(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	b := NewRedisBroker(mr.Addr(), "", 0)
	defer b.Close()

	ctx := context.Background()
	if err := b.EnsureConsumerGroup(ctx, "orders.events", "g1"); err != nil {
		t.Fatalf("ensure group: %v", err)
	}
	// busygroup path
	if err := b.EnsureConsumerGroup(ctx, "orders.events", "g1"); err != nil {
		t.Fatalf("ensure group busy: %v", err)
	}

	if err := b.Publish(ctx, "orders.events", BrokerMessage{
		Key:           "k1",
		Payload:       []byte(`{"order_id":1}`),
		TraceID:       "t1",
		CorrelationID: "c1",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msgs, err := b.Read(ctx, "orders.events", "g1", "c1", 10, 10*time.Millisecond)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("msgs=%v err=%v", msgs, err)
	}
	if msgs[0].TraceID != "t1" || msgs[0].CorrelationID != "c1" {
		t.Fatalf("msg=%+v", msgs[0])
	}

	if err := b.Ack(ctx, "orders.events", "g1", msgs[0].ID); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := b.Ack(ctx, "orders.events", "g1"); err != nil {
		t.Fatalf("ack empty: %v", err)
	}

	empty, err := b.Read(ctx, "orders.events-empty", "g-empty", "c1", 1, 1*time.Millisecond)
	if err == nil {
		t.Fatal("expected group missing error")
	}
	_ = empty

	if err := b.EnsureConsumerGroup(ctx, "orders.events-empty", "g-empty"); err != nil {
		t.Fatalf("ensure empty group: %v", err)
	}
	empty, err = b.Read(ctx, "orders.events-empty", "g-empty", "c1", 1, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("empty read err: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected no messages, got %d", len(empty))
	}
}

func TestRedisBrokerErrors(t *testing.T) {
	bad := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	b := NewRedisBrokerWithClient(bad)
	defer b.Close()
	ctx := context.Background()

	if err := b.Publish(ctx, "orders.events", BrokerMessage{Payload: []byte(`{}`)}); err == nil {
		t.Fatal("expected publish error")
	}
	if _, err := b.Read(ctx, "orders.events", "g", "c", 1, 1*time.Millisecond); err == nil {
		t.Fatal("expected read error")
	}
	if err := b.Ack(ctx, "orders.events", "g", "1-0"); err == nil {
		t.Fatal("expected ack error")
	}
	if err := b.EnsureConsumerGroup(ctx, "orders.events", "g"); err == nil {
		t.Fatal("expected group error")
	}

	if asString(123) != "123" {
		t.Fatalf("asString int mismatch")
	}
	if asString("x") != "x" {
		t.Fatalf("asString string mismatch")
	}

	_ = errors.New
	_ = strings.Contains
}
