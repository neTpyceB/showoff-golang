CREATE TABLE IF NOT EXISTS outbox_events (
    id BIGSERIAL PRIMARY KEY,
    aggregate_type TEXT NOT NULL,
    aggregate_id BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    trace_id TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'published', 'dead')),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_publish_queue
    ON outbox_events (status, next_attempt_at, id);

CREATE TABLE IF NOT EXISTS event_consumer_dlq (
    id BIGSERIAL PRIMARY KEY,
    stream TEXT NOT NULL,
    message_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    error_text TEXT NOT NULL,
    attempts INT NOT NULL,
    trace_id TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS order_event_projection (
    order_id BIGINT PRIMARY KEY,
    last_event_type TEXT NOT NULL,
    payment_status TEXT NOT NULL,
    total_cents BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
