package eventpipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

var nowFn = time.Now

type OutboxEvent struct {
	ID            int64
	EventType     string
	AggregateType string
	AggregateID   int64
	Payload       []byte
	TraceID       string
	CorrelationID string
	Attempts      int
}

type OutboxRepository interface {
	FetchPending(ctx context.Context, limit int, now time.Time) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, id int64, publishedAt time.Time) error
	MarkRetry(ctx context.Context, id int64, attempts int, nextAttemptAt time.Time, errText string) error
	MarkDead(ctx context.Context, id int64, attempts int, errText string) error
}

type BrokerMessage struct {
	Key           string
	Payload       []byte
	TraceID       string
	CorrelationID string
}

type BrokerPublisher interface {
	Publish(ctx context.Context, topic string, m BrokerMessage) error
}

type PublisherConfig struct {
	Topic         string
	BatchSize     int
	MaxAttempts   int
	BaseBackoff   time.Duration
	PollInterval  time.Duration
	ServiceName   string
	LoggerPrintf  func(format string, args ...any)
	DisableTicker bool
}

type Publisher struct {
	repo   OutboxRepository
	broker BrokerPublisher
	cfg    PublisherConfig
}

func NewPublisher(repo OutboxRepository, broker BrokerPublisher, cfg PublisherConfig) *Publisher {
	if cfg.Topic == "" {
		cfg.Topic = "orders.events"
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 500 * time.Millisecond
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "outbox-publisher"
	}
	if cfg.LoggerPrintf == nil {
		cfg.LoggerPrintf = log.Printf
	}
	return &Publisher{repo: repo, broker: broker, cfg: cfg}
}

func (p *Publisher) RunOnce(ctx context.Context) (int, error) {
	events, err := p.repo.FetchPending(ctx, p.cfg.BatchSize, nowFn().UTC())
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, ev := range events {
		msg := BrokerMessage{
			Key:           fmt.Sprintf("%s:%d", ev.AggregateType, ev.AggregateID),
			Payload:       ev.Payload,
			TraceID:       ev.TraceID,
			CorrelationID: ev.CorrelationID,
		}
		if err := p.broker.Publish(ctx, p.cfg.Topic, msg); err != nil {
			nextAttempts := ev.Attempts + 1
			if nextAttempts >= p.cfg.MaxAttempts {
				if markErr := p.repo.MarkDead(ctx, ev.ID, nextAttempts, err.Error()); markErr != nil {
					return processed, markErr
				}
				p.cfg.LoggerPrintf("svc=%s outbox_dead event_id=%d trace_id=%s correlation_id=%s err=%v", p.cfg.ServiceName, ev.ID, ev.TraceID, ev.CorrelationID, err)
			} else {
				backoff := p.cfg.BaseBackoff * time.Duration(1<<min(nextAttempts-1, 8))
				if markErr := p.repo.MarkRetry(ctx, ev.ID, nextAttempts, nowFn().UTC().Add(backoff), err.Error()); markErr != nil {
					return processed, markErr
				}
				p.cfg.LoggerPrintf("svc=%s outbox_retry event_id=%d attempts=%d trace_id=%s correlation_id=%s err=%v", p.cfg.ServiceName, ev.ID, nextAttempts, ev.TraceID, ev.CorrelationID, err)
			}
			processed++
			continue
		}
		if err := p.repo.MarkPublished(ctx, ev.ID, nowFn().UTC()); err != nil {
			return processed, err
		}
		p.cfg.LoggerPrintf("svc=%s outbox_published event_id=%d trace_id=%s correlation_id=%s", p.cfg.ServiceName, ev.ID, ev.TraceID, ev.CorrelationID)
		processed++
	}
	return processed, nil
}

func (p *Publisher) Run(ctx context.Context) error {
	if p.cfg.DisableTicker {
		_, err := p.RunOnce(ctx)
		return err
	}
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := p.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type BrokerConsumedMessage struct {
	ID            string
	Payload       []byte
	TraceID       string
	CorrelationID string
}

type BrokerConsumer interface {
	Read(ctx context.Context, stream string, group string, consumer string, count int, block time.Duration) ([]BrokerConsumedMessage, error)
	Ack(ctx context.Context, stream string, group string, ids ...string) error
}

type DLQRepository interface {
	InsertDLQ(ctx context.Context, stream, messageID string, payload []byte, errText string, attempts int, traceID, correlationID string, createdAt time.Time) error
}

type EventHandler interface {
	Handle(ctx context.Context, payload []byte) error
}

type ConsumerConfig struct {
	Stream       string
	Group        string
	ConsumerName string
	ReadCount    int
	BlockTimeout time.Duration
	MaxAttempts  int
	ServiceName  string
	LoggerPrintf func(format string, args ...any)
}

type Consumer struct {
	broker   BrokerConsumer
	dlq      DLQRepository
	handler  EventHandler
	attempts map[string]int
	cfg      ConsumerConfig
}

func NewConsumer(broker BrokerConsumer, dlq DLQRepository, handler EventHandler, cfg ConsumerConfig) *Consumer {
	if cfg.Stream == "" {
		cfg.Stream = "orders.events"
	}
	if cfg.Group == "" {
		cfg.Group = "orders-consumers"
	}
	if cfg.ConsumerName == "" {
		cfg.ConsumerName = "consumer-1"
	}
	if cfg.ReadCount <= 0 {
		cfg.ReadCount = 50
	}
	if cfg.BlockTimeout <= 0 {
		cfg.BlockTimeout = 2 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "event-consumer"
	}
	if cfg.LoggerPrintf == nil {
		cfg.LoggerPrintf = log.Printf
	}
	return &Consumer{
		broker:   broker,
		dlq:      dlq,
		handler:  handler,
		cfg:      cfg,
		attempts: map[string]int{},
	}
}

func (c *Consumer) RunOnce(ctx context.Context) (int, error) {
	msgs, err := c.broker.Read(ctx, c.cfg.Stream, c.cfg.Group, c.cfg.ConsumerName, c.cfg.ReadCount, c.cfg.BlockTimeout)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, m := range msgs {
		c.attempts[m.ID]++
		err := c.handler.Handle(ctx, m.Payload)
		if err == nil {
			if ackErr := c.broker.Ack(ctx, c.cfg.Stream, c.cfg.Group, m.ID); ackErr != nil {
				return count, ackErr
			}
			delete(c.attempts, m.ID)
			c.cfg.LoggerPrintf("svc=%s event_consumed msg_id=%s trace_id=%s correlation_id=%s", c.cfg.ServiceName, m.ID, m.TraceID, m.CorrelationID)
			count++
			continue
		}
		if c.attempts[m.ID] >= c.cfg.MaxAttempts {
			if dlqErr := c.dlq.InsertDLQ(ctx, c.cfg.Stream, m.ID, m.Payload, err.Error(), c.attempts[m.ID], m.TraceID, m.CorrelationID, nowFn().UTC()); dlqErr != nil {
				return count, dlqErr
			}
			if ackErr := c.broker.Ack(ctx, c.cfg.Stream, c.cfg.Group, m.ID); ackErr != nil {
				return count, ackErr
			}
			delete(c.attempts, m.ID)
			c.cfg.LoggerPrintf("svc=%s event_dead_letter msg_id=%s attempts=%d trace_id=%s correlation_id=%s err=%v", c.cfg.ServiceName, m.ID, c.cfg.MaxAttempts, m.TraceID, m.CorrelationID, err)
		} else {
			c.cfg.LoggerPrintf("svc=%s event_retry msg_id=%s attempts=%d trace_id=%s correlation_id=%s err=%v", c.cfg.ServiceName, m.ID, c.attempts[m.ID], m.TraceID, m.CorrelationID, err)
		}
		count++
	}
	return count, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type OrderCreatedEvent struct {
	OrderID        int64  `json:"order_id"`
	UserID         int64  `json:"user_id"`
	Status         string `json:"status"`
	TotalCents     int64  `json:"total_cents"`
	PaymentStatus  string `json:"payment_status"`
	CorrelationID  string `json:"correlation_id"`
	OccurredAtRFC9 string `json:"occurred_at"`
}

type ProjectionRepository interface {
	UpsertOrderProjection(ctx context.Context, e OrderCreatedEvent, updatedAt time.Time) error
}

type ProjectionHandler struct {
	repo ProjectionRepository
}

func NewProjectionHandler(repo ProjectionRepository) *ProjectionHandler {
	return &ProjectionHandler{repo: repo}
}

func (h *ProjectionHandler) Handle(ctx context.Context, payload []byte) error {
	var e OrderCreatedEvent
	if err := json.Unmarshal(payload, &e); err != nil {
		return err
	}
	if e.OrderID <= 0 {
		return errors.New("invalid order_id")
	}
	return h.repo.UpsertOrderProjection(ctx, e, nowFn().UTC())
}
