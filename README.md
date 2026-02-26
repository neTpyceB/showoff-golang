# showoff-golang

Docker-first Go project with a production-style repository layout.

## Repository Layout

- `cmd/` -> executable entrypoints (one folder per app/service)
- `internal/` -> private application code (only importable inside this module)
- `pkg/` -> reusable libraries (optional; add when needed)

Current entrypoint:

- `cmd/app` -> HTTP server
- `cmd/app` -> HTTP JSON service (`/hello`, `/health`, `/tasks`)
- `cmd/backupsum` -> CLI backup + checksum tool
- `cmd/scrapexport` -> CLI web scraper + parser + exporter

Future binaries can be added without refactoring the current app, for example:

- `cmd/api`
- `cmd/worker`
- `cmd/migrate`

## Features

- Docker-only Go workflow (no local Go install required)
- Hot reload inside Docker using `air`
- Local HTTP server on `localhost:8080`
- `GET /hello` and `GET /health` JSON endpoints
- In-memory Notes/Tasks REST API (CRUD)
- Postgres-backed Notes/Tasks REST API (SQL CRUD + migrations)
- HTTP middleware (request ID + request logging)
- CLI file backup + SHA-256 checksum with JSON report
- CLI web scraping with parsed fields + CSV/JSON export
- Tests + coverage in container
- CI via GitHub Actions
- `make` shortcuts for common commands

## Requirements

- Docker Desktop (or Docker Engine + Compose plugin)
- `make` (optional, for shortcuts)

## Local Infrastructure

- `app` -> Go app/test container
- `db` -> PostgreSQL container (`postgres:18-alpine`)

## Start Development Server (Hot Reload)

```bash
docker compose up --build app
```

Open:

```bash
curl http://localhost:8080/hello
curl http://localhost:8080/health
curl http://localhost:8080/tasks
```

Project doc:

- [`docs/projects/http-hello-health.md`](/Users/vadimsduboiss/Codebase/showoff-golang/docs/projects/http-hello-health.md)
- [`docs/projects/notes-tasks-api.md`](/Users/vadimsduboiss/Codebase/showoff-golang/docs/projects/notes-tasks-api.md)
- [`docs/projects/notes-tasks-api-postgres.md`](/Users/vadimsduboiss/Codebase/showoff-golang/docs/projects/notes-tasks-api-postgres.md)

Example `/hello` response:

```text
{"data":{"message":"Hello from Go (running in Docker)!"},"meta":{"request_id":"req-000001"}}
```

Edit any `.go` file and `air` will rebuild/restart the server automatically.

Create a task example:

```bash
curl -X POST http://localhost:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Buy milk","note":"2 liters","done":false}'
```

The app uses Postgres automatically when `DATABASE_URL` is present (configured in `docker-compose.yml`).

## Run Go Commands Inside Docker

### Run `backupsum` CLI

```bash
docker compose run --rm app go run ./cmd/backupsum -src ./tmp/demo-src -dst ./tmp/demo-backup -report ./tmp/demo-report.json
```

Detailed usage:

- [`docs/projects/backupsum.md`](/Users/vadimsduboiss/Codebase/showoff-golang/docs/projects/backupsum.md)

### Run `scrapexport` CLI

```bash
docker compose run --rm app go run ./cmd/scrapexport \
  -url https://example.com \
  -json ./tmp/scrape-report.json \
  -csv ./tmp/scrape-report.csv
```

Detailed usage:

- [`docs/projects/scrapexport.md`](/Users/vadimsduboiss/Codebase/showoff-golang/docs/projects/scrapexport.md)

### Run app once (no hot reload)

```bash
docker compose run --rm --service-ports app go run ./cmd/app
```

### Run tests

```bash
docker compose run --rm app go test ./...
```

Includes Postgres integration tests (Compose starts the `db` service automatically for the `app` service).

### Run tests with coverage

```bash
docker compose run --rm app go test ./... -covermode=count -coverprofile=coverage.out
docker compose run --rm app go tool cover -func=coverage.out
```

### Build binary

```bash
docker compose run --rm app go build -buildvcs=false -o ./bin/app ./cmd/app
```

Run the built binary inside the container (Linux binary):

```bash
docker compose run --rm --service-ports app ./bin/app
```

### Format code

```bash
docker compose run --rm app gofmt -w .
```

### Arbitrary Go commands

```bash
docker compose run --rm app go env
docker compose run --rm app go list ./...
docker compose run --rm app go test ./internal/hello -v
```

### Interactive shell

```bash
docker compose run --rm app sh
```

## Makefile Shortcuts

```bash
make run
make test
make cover
make build
make build-all
make backupsum-build
make scrapexport-build
make fmt
make shell
```

## Hot Reload Configuration

- Tool: `air` (`github.com/air-verse/air`)
- Config file: `.air.toml`
- Build output (temporary): `tmp/app`

## CI

GitHub Actions workflow (`.github/workflows/ci.yml`) does:

- Docker image build
- `gofmt` check
- tests with coverage
- `100%` coverage enforcement
- command package build (`-buildvcs=false`)
