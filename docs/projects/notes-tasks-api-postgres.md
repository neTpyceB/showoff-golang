# Notes/Tasks REST API + Postgres

Notes/Tasks REST API backed by PostgreSQL (separate Docker container).

## Architecture (practical)

- HTTP handlers: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/handler.go`
- Task API + repository interface: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/tasks.go`
- Postgres repository (SQL CRUD + migrations): `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/tasks_postgres.go`
- SQL migrations: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/migrations/`

## Docker Compose Services

- `app` (Go app / tests / hot reload)
- `db` (Postgres, separate container)

`app` gets:

- `DATABASE_URL`
- `TEST_DATABASE_URL`

## Migrations

Migrations are SQL files executed by the app repository on startup.

Current migration:

- `001_tasks.sql` -> creates `tasks` table + index

Behavior:

- migrations are applied automatically when Postgres repository starts
- SQL is written idempotently (`IF NOT EXISTS`) for safe repeated startup

## Repository (SQL CRUD)

Implemented methods:

- `List(ctx)`
- `Create(ctx, input, ts)`
- `Get(ctx, id)`
- `Update(ctx, id, input, ts)`
- `Delete(ctx, id)`

## Integration Tests

Integration tests run against real Postgres using `TEST_DATABASE_URL`.

Coverage includes:

- migration startup + idempotent rerun
- SQL CRUD flow
- not-found cases

Additional `sqlmock` tests cover DB driver/error branches (scan/rows errors, migration errors, etc.) so repo coverage remains `100%`.

## Run

```bash
make run
```

## API checks

```bash
curl http://localhost:8080/tasks
```

```bash
curl -X POST http://localhost:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Buy milk","note":"2 liters","done":false}'
```

## Tests

```bash
make cover
```
