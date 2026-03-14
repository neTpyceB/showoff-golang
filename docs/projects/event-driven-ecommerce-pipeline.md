# Event-driven E-commerce Pipeline

## Services

- `app`: API + transactional order writes + outbox insert
- `outbox-publisher`: polls Postgres outbox and publishes to Redis Stream
- `event-consumer`: consumes stream, retries, and writes DLQ/projections

## Core Tables

- `outbox_events`
- `event_consumer_dlq`
- `order_event_projection`

Migration: `internal/httpapp/migrations/005_event_pipeline.sql`

## Reliability Patterns

- Transactional outbox (order + outbox in same DB transaction)
- Publisher retries with exponential backoff
- Dead marking after max attempts
- Consumer retries with in-memory attempt tracking
- Consumer DLQ persistence after max attempts

## Correlation / Tracing Fields

- `trace_id`
- `correlation_id` (uses order idempotency key)

Each publisher/consumer log line includes both values.

## Local Run

```bash
docker compose up --build app outbox-publisher event-consumer
```

## Operational Notes

- Stream default: `orders.events`
- Consumer group default: `orders-consumers`
- Deploy-ready split in one repo via `cmd/app`, `cmd/outboxpublisher`, `cmd/eventconsumer`
