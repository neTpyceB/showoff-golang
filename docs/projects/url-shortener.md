# URL Shortener Service (API + redirect)

Short URL creation and redirect service implemented in the main HTTP app.

## Endpoints

- `POST /short-urls`
- `GET /short-urls/{code}`
- `GET /{code}`

## Request and Response

### Create short URL (`POST /short-urls`)

Request body:

```json
{
  "url": "https://go.dev/",
  "code": "go-docs"
}
```

Notes:

- `url` is required and must be absolute `http` or `https`.
- `code` is optional.
- If `code` is omitted, an 8-char code is generated.
- Code validation: `4..32` chars, allowed `a-z A-Z 0-9 _ -`.

Successful response (`201`):

```json
{
  "data": {
    "short_url": {
      "code": "go-docs",
      "target_url": "https://go.dev/",
      "short_path": "/go-docs",
      "created_at": "2026-03-13T10:00:00Z"
    }
  },
  "meta": {
    "request_id": "req-000001"
  }
}
```

Error responses:

- `400` `validation_error`
- `400` `invalid_json`
- `409` `code_conflict`

### Get short URL metadata (`GET /short-urls/{code}`)

Returns the same `short_url` shape.

Error responses:

- `400` `invalid_code`
- `404` `not_found`

### Redirect (`GET /{code}`)

Returns `302 Found` with `Location` header set to the target URL.

Error responses:

- `400` `invalid_code`
- `404` `not_found`

## Storage

- In-memory repo for default handler.
- Postgres repo when `DATABASE_URL` is set.
- Migration file: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/migrations/002_short_urls.sql`.

## Commands

```bash
make run
make test
make cover
```
