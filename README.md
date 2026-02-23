## Standard Go Layout (Practical Subset)

- `cmd/` -> executable entrypoints (one folder per app/service)
- `internal/` -> private app code (importable only inside this module)
- `pkg/` -> reusable public libraries (optional; not needed yet)

Current entrypoint:

- `cmd/app` -> main executable for this project

This structure lets you add more binaries later, for example:

- `cmd/api`
- `cmd/worker`
- `cmd/migrate`

## What this project currently supports

- Run Go entirely inside Docker (no local Go install)
- Run the app
- Run tests
- Build a binary
- Format code
- Open an interactive shell in the Go container
- Use `make` shortcuts for common commands

## Requirements

- Docker Desktop (or Docker Engine + Compose plugin)

No local Go installation is needed.

## Run the Project

```bash
docker compose up --build app
```

Expected output:

```text
Hello from Go (running in Docker)!
```

## Run Go Commands Without Local Go Install

All commands below run inside the container.

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

Run the built binary inside the container (it is a Linux binary because it was built in Docker):

```bash
docker compose run --rm app ./bin/app
```

### Format code

```bash
docker compose run --rm app gofmt -w .
```

### Run arbitrary Go commands

```bash
docker compose run --rm app go env
docker compose run --rm app go list ./...
docker compose run --rm app go test ./internal/hello -v
```

### Interactive shell inside container

```bash
docker compose run --rm app sh
```

Inside the shell you can run Go commands directly, for example:

```sh
go version
go test ./...
go run ./cmd/app
```

## Makefile Shortcuts (optional but practical)

You can use `make` on your Mac to run Docker commands for you (still no local Go needed):

```bash
make run
make test
make cover
make build
make fmt
make shell
```

## GitHub Actions (Auto Tests)

The workflow at `.github/workflows/ci.yml`:

- builds the Docker image
- checks formatting with `gofmt`
- runs tests with coverage
- enforces `100%` coverage for the current lesson
- builds the binary

## Why this structure is useful

- Separates executable entrypoint (`cmd/app`) from business logic (`internal/hello`)
- Scales to multiple services/binaries later
- Keeps Docker workflow consistent for local development and CI
