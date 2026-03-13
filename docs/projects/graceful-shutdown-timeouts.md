# Graceful Shutdown + Timeouts Baseline

Baseline reliability controls for the HTTP service.

## Implemented

- Graceful shutdown in `/Users/vadimsduboiss/Codebase/showoff-golang/cmd/app/main.go`:
  - Uses `signal.NotifyContext` for `SIGINT` and `SIGTERM`.
  - Runs server with `http.Server` instead of `http.ListenAndServe`.
  - Calls `Shutdown` with timeout (`10s`) to drain in-flight requests.
  - Handles `http.ErrServerClosed` as normal shutdown.

- Request timeout middleware in `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/handler.go`:
  - Wraps each request context with `context.WithTimeout`.
  - Baseline timeout is `15s`.
  - Downstream handlers/repositories receive deadline-aware context.

- Server hardening baseline:
  - `ReadHeaderTimeout` set to `5s`.

## Commands

```bash
make run
make test
make cover
```
