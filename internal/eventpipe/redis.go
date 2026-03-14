package eventpipe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisBroker struct {
	client *redis.Client
}

func NewRedisBroker(addr, password string, db int) *RedisBroker {
	return &RedisBroker{
		client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db}),
	}
}

func NewRedisBrokerWithClient(c *redis.Client) *RedisBroker { return &RedisBroker{client: c} }

func (b *RedisBroker) Close() error { return b.client.Close() }

func (b *RedisBroker) EnsureConsumerGroup(ctx context.Context, stream, group string) error {
	err := b.client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (b *RedisBroker) Publish(ctx context.Context, topic string, m BrokerMessage) error {
	_, err := b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: topic,
		Values: map[string]any{
			"payload":        string(m.Payload),
			"trace_id":       m.TraceID,
			"correlation_id": m.CorrelationID,
			"key":            m.Key,
		},
	}).Result()
	if err != nil {
		return fmt.Errorf("redis xadd: %w", err)
	}
	return nil
}

func (b *RedisBroker) Read(ctx context.Context, stream, group, consumer string, count int, block time.Duration) ([]BrokerConsumedMessage, error) {
	res, err := b.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    int64(count),
		Block:    block,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("redis xreadgroup: %w", err)
	}
	out := []BrokerConsumedMessage{}
	for _, st := range res {
		for _, m := range st.Messages {
			out = append(out, BrokerConsumedMessage{
				ID:            m.ID,
				Payload:       []byte(asString(m.Values["payload"])),
				TraceID:       asString(m.Values["trace_id"]),
				CorrelationID: asString(m.Values["correlation_id"]),
			})
		}
	}
	return out, nil
}

func (b *RedisBroker) Ack(ctx context.Context, stream, group string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := b.client.XAck(ctx, stream, group, ids...).Err(); err != nil {
		return fmt.Errorf("redis xack: %w", err)
	}
	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
