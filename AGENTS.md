# AGENTS.md

Minimal instructions for AI coding agents working in this repository.

## Scope

- This file is the main AI agent entrypoint for repo-specific rules.
- Keep this file short and practical.
- Put additional AI docs in `docs/` and link them here.

## Core Rules

- Use the latest stable versions of software unless the user says otherwise.
- Prefer Docker-first workflow; do not require local Go installation.
- Run Go commands via Docker Compose (`docker compose run --rm app ...`) or `make` targets.
- Keep docs minimal: no filler, only actionable instructions.
- Update this file and linked docs when new repo rules/instructions appear.

## Current Project Conventions

- Main executable entrypoint: `cmd/app`
- Private app code: `internal/`
- Build binary command must include: `-buildvcs=false`
- CI container shell commands should use `sh -c` (not `sh -lc`)

## Preferred Commands

- `make run`
- `make test`
- `make cover`
- `make build`
- `make fmt`
- `make shell`

## Linked AI Docs

- [`docs/ai-rules.md`](/Users/vadimsduboiss/Codebase/showoff-golang/docs/ai-rules.md)
