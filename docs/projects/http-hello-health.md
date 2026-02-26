# HTTP `hello + health` service

Small JSON HTTP service with middleware.

## Binary

- Source: `/Users/vadimsduboiss/Codebase/showoff-golang/cmd/app`

## Endpoints

### `GET /hello`

Returns a greeting message.

Example response:

```json
{
  "data": {
    "message": "Hello from Go (running in Docker)!"
  },
  "meta": {
    "request_id": "req-000001"
  }
}
```

### `GET /health`

Returns service health metadata.

Example response:

```json
{
  "data": {
    "status": "ok",
    "service": "showoff-golang",
    "timestamp": "2026-02-26T12:00:00Z"
  },
  "meta": {
    "request_id": "req-000002"
  }
}
```

## Middleware

- Request ID middleware:
  - generates request ID
  - stores it in request context
  - sets `X-Request-ID` response header
- Request logging middleware:
  - logs method, path, status, duration, request ID

## Run (hot reload)

```bash
make run
```

## Manual checks

```bash
curl http://localhost:8080/hello
curl http://localhost:8080/health
```
