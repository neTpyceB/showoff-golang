package eventpipe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PostgresRepo struct {
	db *sql.DB
}

func NewPostgresRepo(db *sql.DB) *PostgresRepo { return &PostgresRepo{db: db} }

func (r *PostgresRepo) FetchPending(ctx context.Context, limit int, now time.Time) ([]OutboxEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, event_type, aggregate_type, aggregate_id, payload::text, trace_id, correlation_id, attempts
		FROM outbox_events
		WHERE status = 'pending' AND next_attempt_at <= $1
		ORDER BY id ASC
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query outbox pending: %w", err)
	}
	defer rows.Close()
	out := []OutboxEvent{}
	for rows.Next() {
		var e OutboxEvent
		var payload string
		if err := rows.Scan(&e.ID, &e.EventType, &e.AggregateType, &e.AggregateID, &payload, &e.TraceID, &e.CorrelationID, &e.Attempts); err != nil {
			return nil, fmt.Errorf("scan outbox row: %w", err)
		}
		e.Payload = []byte(payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox rows: %w", err)
	}
	return out, nil
}

func (r *PostgresRepo) MarkPublished(ctx context.Context, id int64, publishedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'published', published_at = $2, attempts = attempts + 1, last_error = ''
		WHERE id = $1
	`, id, publishedAt)
	if err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	return nil
}

func (r *PostgresRepo) MarkRetry(ctx context.Context, id int64, attempts int, nextAttemptAt time.Time, errText string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'pending', attempts = $2, next_attempt_at = $3, last_error = $4
		WHERE id = $1
	`, id, attempts, nextAttemptAt, errText)
	if err != nil {
		return fmt.Errorf("mark outbox retry: %w", err)
	}
	return nil
}

func (r *PostgresRepo) MarkDead(ctx context.Context, id int64, attempts int, errText string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'dead', attempts = $2, last_error = $3
		WHERE id = $1
	`, id, attempts, errText)
	if err != nil {
		return fmt.Errorf("mark outbox dead: %w", err)
	}
	return nil
}

func (r *PostgresRepo) InsertDLQ(ctx context.Context, stream, messageID string, payload []byte, errText string, attempts int, traceID, correlationID string, createdAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO event_consumer_dlq (stream, message_id, payload, error_text, attempts, trace_id, correlation_id, created_at)
		VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7, $8)
	`, stream, messageID, string(payload), errText, attempts, traceID, correlationID, createdAt)
	if err != nil {
		return fmt.Errorf("insert consumer dlq: %w", err)
	}
	return nil
}

func (r *PostgresRepo) UpsertOrderProjection(ctx context.Context, e OrderCreatedEvent, updatedAt time.Time) error {
	if e.OrderID <= 0 {
		return errors.New("invalid order id")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO order_event_projection (order_id, last_event_type, payment_status, total_cents, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (order_id) DO UPDATE
		SET last_event_type = EXCLUDED.last_event_type,
		    payment_status = EXCLUDED.payment_status,
		    total_cents = EXCLUDED.total_cents,
		    updated_at = EXCLUDED.updated_at
	`, e.OrderID, "order.created", e.PaymentStatus, e.TotalCents, updatedAt)
	if err != nil {
		return fmt.Errorf("upsert projection: %w", err)
	}
	return nil
}
