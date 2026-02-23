# showoff-golang

Run Go entirely inside Docker (no local Go install required):

- run the app
- run tests
- build a binary
- format code

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
docker compose run --rm app go build -buildvcs=false -o ./bin/hello ./cmd/hello
```

Run the built binary inside the container (it is a Linux binary because it was built in Docker):

```bash
docker compose run --rm app ./bin/hello
```

### Format code

```bash
docker compose run --rm app gofmt -w .
```

## GitHub Actions (Auto Tests)

The workflow at `.github/workflows/ci.yml`:

- builds the Docker image
- checks formatting with `gofmt`
- runs tests with coverage
- enforces `100%` coverage for the current lesson
- builds the binary
