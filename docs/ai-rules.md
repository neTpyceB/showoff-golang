# AI Rules (Extended)

Additional repo-specific AI notes. Keep entries short.

## Documentation Policy

- New AI/process docs go under `docs/`.
- Reference new docs from `AGENTS.md`.
- Do not duplicate the same rule across files unless necessary.

## Go + Docker Notes

- Local development is Docker-first.
- Binary built in container is Linux; run it in container unless cross-compiling explicitly.
- Current image pin: `golang:1.26.0` (update when newer stable is adopted).

## CI Notes

- Formatting check runs in container with `gofmt`.
- Coverage is currently enforced to `100%`.

## Public Docs Style

- Do not mention training/lesson progression in public code or docs.
- Write docs as production-project documentation.
