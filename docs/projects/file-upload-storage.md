# File Upload + Storage Service

## Endpoints

- `POST /files` (auth required): multipart upload (`file` form part)
- `GET /files/{id}` (auth required): file metadata

## Upload Request

```bash
curl -X POST http://localhost:8080/files \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@./tmp/demo.txt"
```

## Upload Response (example)

```json
{
  "data": {
    "id": 1,
    "file_name": "demo.txt",
    "content_type": "text/plain",
    "size_bytes": 12,
    "sha256": "…",
    "storage_provider": "disk",
    "storage_key": "20260314T100000/abc123.txt",
    "created_at": "2026-03-14T10:00:00Z"
  },
  "meta": {
    "request_id": "req-000001"
  }
}
```

## Storage Drivers

### Disk (default)

- `FILE_STORAGE_DRIVER=disk`
- `FILE_STORAGE_DIR=./tmp/uploads`

### S3-compatible

- `FILE_STORAGE_DRIVER=s3`
- `FILE_S3_BUCKET=uploads`
- `FILE_S3_REGION=us-east-1`
- `FILE_S3_ENDPOINT=http://minio:9000` (optional for custom endpoint)
- `FILE_S3_ACCESS_KEY=...`
- `FILE_S3_SECRET_KEY=...`
- `FILE_S3_PATH_STYLE=true` (default `true`)

## Metadata Storage

- Postgres table: `uploaded_files`
- Migration: `internal/httpapp/migrations/003_uploaded_files.sql`

## Background Processing Hook

- Every successful upload triggers `OnUploaded` hook asynchronously.
- Current default hook is no-op and ready for integration with queue/worker modules.
