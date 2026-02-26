# Notes/Tasks REST API (in-memory)

JSON REST API for tasks with in-memory storage.

## Binary

- Source: `/Users/vadimsduboiss/Codebase/showoff-golang/cmd/app`

## Endpoints

- `GET /hello`
- `GET /health`
- `GET /tasks`
- `POST /tasks`
- `GET /tasks/{id}`
- `PUT /tasks/{id}`
- `DELETE /tasks/{id}`

## Task Schema

```json
{
  "id": 1,
  "title": "Buy milk",
  "note": "2 liters",
  "done": false,
  "created_at": "2026-02-26T12:00:00Z",
  "updated_at": "2026-02-26T12:00:00Z"
}
```

## Request Bodies

### Create task (`POST /tasks`)

```json
{
  "title": "Buy milk",
  "note": "2 liters",
  "done": false
}
```

### Update task (`PUT /tasks/{id}`)

```json
{
  "title": "Buy milk (done)",
  "note": "Purchased on the way home",
  "done": true
}
```

## Success Response Shape

All success responses use:

```json
{
  "data": {},
  "meta": {
    "request_id": "req-000001"
  }
}
```

## Error Response Shape

```json
{
  "error": {
    "code": "validation_error",
    "message": "request validation failed",
    "fields": {
      "title": "title is required"
    }
  },
  "meta": {
    "request_id": "req-000002"
  }
}
```

## Validation Rules

- `title` is required
- `title` max length: `200`
- `note` max length: `2000`
- request body must be a single JSON object
- unknown JSON fields are rejected

## Middleware

- Request ID middleware:
  - sets `X-Request-ID`
  - injects request ID into request context
- Request logging middleware:
  - logs method/path/status/duration/request_id

## Example Commands

```bash
curl http://localhost:8080/tasks
```

```bash
curl -X POST http://localhost:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Buy milk","note":"2 liters","done":false}'
```

```bash
curl http://localhost:8080/tasks/1
```

```bash
curl -X PUT http://localhost:8080/tasks/1 \
  -H 'Content-Type: application/json' \
  -d '{"title":"Buy milk (done)","note":"Purchased","done":true}'
```

```bash
curl -X DELETE http://localhost:8080/tasks/1
```

## Notes

- Storage is in-memory only (data resets on process restart).
- Designed for HTTP API practice: routing, validation, middleware, structured errors.
