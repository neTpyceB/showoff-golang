# Notes API Auth + RBAC

RBAC and ownership controls added to Notes/Tasks API.

## Scope

- Protected routes:
  - `GET /tasks`
  - `POST /tasks`
  - `GET /tasks/{id}`
  - `PUT /tasks/{id}`
  - `DELETE /tasks/{id}`

- Auth source: JWT middleware (`/auth/*` module)
- Roles:
  - `user`: only own tasks
  - `admin`: all tasks

## Behavior

- Unauthenticated access to `/tasks*` returns `401`.
- Owner checks on get/update/delete:
  - non-owner user -> `403`
  - admin -> allowed
- Listing:
  - user gets only own tasks
  - admin gets all tasks
- Created task is bound to authenticated user (`owner_user_id`).

## Storage

- `tasks` now has `owner_user_id`.
- Migration updated in `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/migrations/001_tasks.sql` with backward-compatible `ADD COLUMN IF NOT EXISTS`.
- Postgres repository CRUD includes owner field.

## Clean Boundaries

- Auth/JWT logic in `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/auth.go`.
- Tasks authorization checks in `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/tasks.go`.
- Route protection wired in `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/handler.go`.
