# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`mokapay` is a Go microservices monorepo with an optional React web frontend. Tasks run through Make: a root `Makefile` for project-global commands, one `Makefile` per app for service-scoped commands (delegated from the root as `make <service>/<target>`). Services communicate via gRPC using Protobuf contracts defined in `proto/`.

## Repository Structure

```
proto/          # Protobuf definitions; generated output in proto/codebase/
core/           # Shared Go library packages (config, db, http, logger, otel)
app/
  services/
    gateway/      # Go service (API gateway) — reference service layout
    <service>/    # one folder per additional Go gRPC microservice
  tooling/
    logfmt/       # Workspace tooling binaries
infra/
  compose/      # docker-compose.yml + postgres init scripts (local dev)
  k8s/          # kustomize manifests (base/ + dev/ overlays, kind config)
docs/           # design docs & plans (git-ignored) — see .claude/rules/docs.md
```

## Commands

### Infrastructure

The compose file lives at `infra/compose/docker-compose.yml`; make targets
export `COMPOSE_FILE` (from `vars.env`) so plain `docker compose` works inside
them.

```bash
make start                    # docker compose up -d --build (stores + all services + UIs + edge nginx)
make stop                     # docker compose down
# manually: docker compose -f infra/compose/docker-compose.yml up -d
```

The compose stack is the deployment: an nginx edge (`proxy`) routes by
hostname (`app.<domain>` → mobile web app, `admin.<domain>` → back-office,
`${MOKAPAY_DOMAIN:-localhost}`) and by port for single-hostname frontings —
host port `${MOKAPAY_HTTP_PORT:-80}` defaults to the mobile app, host port
`${MOKAPAY_ADMIN_HTTP_PORT:-81}` to the admin app. `/v1/*` on any of them →
the gateway (same-origin API, no CORS). Tailscale deployment: `tailscale
serve --bg --https=443 http://localhost:80` (mobile) + `tailscale serve
--bg --https=8443 http://localhost:81` (admin). Frontends build from the
shared `infra/compose/ui/Dockerfile`. Object storage is real S3 (file
service `.env`) — there is no MinIO.

### Running a Go Service

```bash
make gateway/serve               # Runs service, pipes logs through logfmt formatter
# or directly:
cd app/services/gateway && go run . run
cd app/services/<service> && go run . run --migrate   # Also runs DB migrations on startup
```

### Database Migrations (per service with a DB)

```bash
make <service>/migrate   # Run migrations only
make <service>/seed      # Seed data
make <service>/reset     # Reset DB
```

### Building

```bash
make gateway/check          # Compile-check every package of a Go service
make gateway/build          # Build the service binary into app/services/gateway/bin/
make gateway/image          # Build the service's docker image
make build                  # Build all docker images
make check-all              # Compile-check all Go services
```

### Testing

```bash
make gateway/test           # Test a Go service
make test-all               # Test all Go services
```

### Linting / Formatting

```bash
make gateway/lint           # go vet + go fmt
make gateway/tidy           # go mod tidy
```

### Protobuf Code Generation

```bash
make proto                  # buf generate → regenerates proto/codebase/
```

## Go Module Naming

The repo is a **single Go module**: `mokapay.app` (root `go.mod`, no `go.work`,
no `replace` directives). Import paths are directory paths:
- `mokapay.app/core/<pkg>` — the shared core library
- `mokapay.app/proto/codebase/...` — generated protobuf code
- `mokapay.app/app/services/<service>/...` — service code (`internal/` keeps
  services isolated from each other)

## Go Service Architecture

Every Go service follows the same pattern (a MongoDB-backed service has no SQL
directories; its `migrate` ensures collections + `$jsonSchema` validators +
indexes, `seed` inserts starter docs, `reset` drops the database):

- **`cmd/`** — Cobra CLI commands: `root`, `run`, `migrate`, `seed`, `reset`. The `run` command accepts `--migrate` to run migrations before starting.
- **root `internal` package** (`internal/app.go`, `config.go`, `migrate.go`, …) — composition layer: `Run()` wires logger → DB → server, registers handlers, blocks on shutdown. Imports only go up: `internal` → `internal/api` → `internal/business` → `core/`.
- **`internal/api/<domain>/`** — transport layer: receives requests, validates, calls the usecase; proto↔business mapping lives in `internal/api/<domain>/mapper/` (free functions). The gateway's routes live in `internal/api/api.go` (`api.Register`).
- **`internal/business/<domain>/`** — business types at the package root (read entity, `NewX`/`UpdateX`/`XFilter`, enums); logic in `internal/business/<domain>/usecase/` (`usecase.UseCase`); persistence in `internal/business/<domain>/repo/` (`repo.Store` — owns its Mongo/SQL models, takes and returns business types). Flow: api → usecase → repo.
- **`internal/sdk/`** — service-specific foundation packages (gateway: `errs`, `protocodec`); `internal/integration/` — third-party vendor clients. An sdk package used by a second service moves to `/core`.
- **`internal/db/db.go`** (Postgres services) — embeds four SQL directories via `//go:embed`: `scripts/`, `migrations/`, `view/`, `procedure/`.

### DB Migration Order (Postgres services)

When `Migrate()` is called it applies SQL in this sequence:
1. `internal/db/scripts/` — init scripts (one transaction)
2. `internal/db/migrations/` — versioned migrations via `sql-migrate`
3. `internal/db/view/` and `internal/db/procedure/` — views and stored procedures (one transaction)

## Core Library (`core/`)

Shared infrastructure packages, imported as `mokapay.app/core/<pkg>`:

| Package | Purpose |
|---------|---------|
| `core/logger` | Structured JSON logger built on `log/slog`. Accepts a `TraceIDFn` to inject trace IDs into every log line. Use `NewWithEvents` to attach callbacks on error/warn. |
| `core/db` | PostgreSQL (`Postgres`/`PostgresConfig`) and MongoDB (`Mongo`/`MongoConfig`) wrappers. `Begin()` runs work in a transaction with automatic rollback. |
| `core/http` | HTTP server mini-framework (`HandlerFunc = func(ctx, *http.Request) Encoder`, stdlib ServeMux routing, middleware, debug server). |
| `core/otel` | OpenTelemetry tracing. Pass an empty `Host` in `Config` to get a no-op tracer. `GetTraceID` is passed to the logger as `TraceIDFn`. |
| `core/config` | Env-var helpers: `GetEnv`, `GetEnvAsInt`, `GetEnvAsTimeDuration`, `GetEnvAsSlice`. All service config resolves through these. |
| `app/tooling/logfmt` | Standalone binary that reads JSON log lines from stdin and pretty-prints them. Used automatically by the `serve` make targets. |

## Workflow (verifying changes)

@.claude/rules/workflow.md

## Design docs & documentation

@.claude/rules/docs.md

## Go coding style

@.claude/rules/go-coding-style.md

## SQL & Database rules

@.claude/rules/sql.md

## Frontend structure

@.claude/rules/frontend-structure.md
(applies once the project adds a frontend under `app/ui/`)

## Environment Variables

Docker Compose is for local development only; the Postgres credentials are hardcoded directly in `infra/compose/docker-compose.yml` (no root `.env`). Each service has its own `app/services/<service>/.env`, loaded via `env_file:` in compose and read at runtime through `core/config`:

```
DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME          (Postgres services)
MONGO_HOST, MONGO_PORT, MONGO_USER, MONGO_PASSWORD, MONGO_NAME  (MongoDB services)
GRPC_PORT  (default: :50051)
```
