# Metrics/Log Collector Service

Concurrent ingestion pipeline for application metrics with background aggregation and metrics endpoint exposure.

## Location

- Implementation: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/metricscollector/service.go`
- Tests: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/metricscollector/service_test.go`

## Features

- In-process ingestion queue (`chan Event`) with backpressure
- Worker pool for concurrent event processing
- Periodic flush loop for batched aggregate writes
- Aggregate storage via pluggable store:
  - In-memory store
  - Redis store (`HINCRBY`-based counters/latency aggregates)
- HTTP instrumentation middleware for automatic request metrics
- Metrics API:
  - `POST /metrics/events`
  - `GET /metrics`

## Event Model

```json
{
  "source": "http",
  "name": "GET:/tasks",
  "status": "200",
  "duration_ms": 12
}
```

## Runtime Integration

`cmd/app` now runs the collector in background and exposes `/metrics` routes. If `METRICS_REDIS_ADDR` is set, aggregates are persisted in Redis; otherwise memory store is used.

## Env Vars

- `METRICS_REDIS_ADDR` (optional)
- `METRICS_REDIS_PASSWORD` (optional)

## Commands

```bash
make run
make test
make cover
```
