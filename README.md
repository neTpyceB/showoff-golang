# showoff-golang

Docker-first Go project with a production-style repository layout.

## Repository Layout

- `cmd/` -> executable entrypoints (one folder per app/service)
- `internal/` -> private application code (only importable inside this module)
- `pkg/` -> reusable libraries (optional; add when needed)

Current entrypoint:

- `cmd/app` -> HTTP server

Future binaries can be added without refactoring the current app, for example:

- `cmd/api`
- `cmd/worker`
- `cmd/migrate`

## Features

- Docker-only Go workflow (no local Go install required)
- Hot reload inside Docker using `air`
- Local HTTP server on `localhost:8080`
- Tests + coverage in container
- CI via GitHub Actions
- `make` shortcuts for common commands

## Requirements

- Docker Desktop (or Docker Engine + Compose plugin)
- `make` (optional, for shortcuts)

## Start Development Server (Hot Reload)

```bash
docker compose up --build app
```

Open:

```bash
curl http://localhost:8080/
```

Expected response:

```text
Hello from Go (running in Docker)!
```

Edit any `.go` file and `air` will rebuild/restart the server automatically.

## Run Go Commands Inside Docker

### Run app once (no hot reload)

```bash
docker compose run --rm --service-ports app go run ./cmd/app
```

### Run tests

```bash
docker compose run --rm app go test ./...
```

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
- binary build (`-buildvcs=false`)
