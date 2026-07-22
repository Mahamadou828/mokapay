---
name: add-service
description: Scaffold a new Go microservice in this workspace and wire it into the Makefiles, compose, and k8s. Use when asked to add, create, or scaffold a new service (gRPC backend, worker, or gateway-fronted domain service).
---

# Add a new service

Scaffold `app/services/<name>` following the workspace architecture and wire it
into every place the workspace expects it. The **gateway**
(`app/services/gateway/`) is the living reference for the composition layer,
Makefile, and Dockerfile — copy its shapes, don't invent new ones.

Names are short, lowercase, singular domain nouns (`billing`, `identity`,
`catalog`).

## 0. Load the rules

Read these first; everything scaffolded must follow them:

- `CLAUDE.md` — "Go Service Architecture" (the per-service skeleton).
- `.claude/rules/go-coding-style.md` — layer stack, signatures, value semantics.
- `.claude/rules/sql.md` — only if the service uses Postgres.
- For the proto contract, invoke the **api-design** skill; for the schema, the
  **db-design** skill.

Settle with the user before scaffolding: does the service have persistence
(Postgres / MongoDB / none), and is it fronted by the gateway?

## 1. Proto contract (gRPC services)

- `proto/<name>/v1/<aggregate>_service.proto` + `shared_message.proto`,
  package `<name>.v1` (see api-design skill for RPC/message rules).
- Run `make proto` — generated code lands in `proto/codebase/<name>/v1/`,
  imported as `template.app/proto/codebase/<name>/v1`.

## 2. Service tree

```
app/services/<name>/
  main.go               # package main; calls cmd.Execute() (copy gateway's)
  cmd/                  # Cobra: root.go + run.go; migrate/seed/reset if it has a DB
  internal/
    app.go              # Run(): config → logger → DB → server → handlers → shutdown
    config.go           # Config struct + newLogger() (copy gateway's shape)
    api/<domain>/       # transport handlers + mapper/ (proto ↔ business)
    business/<domain>/  # business types; usecase/; repo/
  db/                   # Postgres only: scripts/ migrations/ view/ procedure/ query/ + db.go (//go:embed)
  Makefile              # copy gateway's; change SERVICE, IMAGE, APP vars
  Dockerfile            # copy gateway's; change service paths and binary name
  .env + .env.example   # config read through core/config.GetEnv*
```

- Composition layer: adapt the gateway's `internal/app.go` — same
  startup/shutdown choreography (config from env, `newLogger()`, debug server,
  serverErrors/shutdown channels, graceful stop). For a gRPC service the HTTP
  server is replaced by a `grpc.Server` listening on `GRPC_PORT`
  (default `:50051`).
- DB wiring: `core/db` (`NewPostgres`/`NewMongo`, `Connect(ctx)`); config keys
  `DB_*` (Postgres) or `MONGO_*` (MongoDB) as listed in CLAUDE.md.
- `run` accepts `--migrate` to run migrations before serving.
- If the gateway will call this service, also add a `client/` package at the
  service root (`client.New(target string)` → struct holding the
  `*grpc.ClientConn` and the generated typed clients) — this is the only
  package other services may import from it.

## 3. Workspace wiring (every item, in this order)

1. **`vars.env`** — add `<NAME>_APP=<name>` and
   `<NAME>_IMAGE=${BASE_IMAGE_NAME}/${<NAME>_APP}:${VERSION}`.
2. **Root `Makefile`** —
   - add `<name>` to `GO_SERVICES`;
   - add a delegation rule (one per app, no shared pattern rule):
     ```make
     <name>/%:
     	$(MAKE) -C app/services/<name> $(@F)
     ```
   - append `<name>/image` to `build`;
   - add the image to `kind-load-images` and `<name>/restart-dev` to
     `kind-restart`.
3. **Compose** (`infra/compose/docker-compose.yml`) — service entry modeled on
   `gateway`'s: build context `../..`, its Dockerfile, `template.app` network
   (plus `template.infrastructure` if it talks to postgres/mongo/redis
   directly), `env_file`, ports, `depends_on` with `condition:
   service_healthy` on the stores it uses. DB-backed services append
   `command: ["--migrate"]`.
4. **Postgres init** (`infra/compose/postgres/init/01-create-databases.sql`) —
   Postgres services only: `CREATE DATABASE <name>;`.
5. **k8s** — copy `infra/k8s/base/gateway/` → `base/<name>/` and
   `dev/gateway/` → `dev/<name>/`; rename metadata/labels/image (image name
   `<name>-image`, dev override `localhost/template/<name>`), set container
   ports and probes (gRPC services: TCP or exec probes instead of HTTP).
   Add a host port mapping in `dev/kind-config.yaml` if it must be reachable
   from the host.
6. **Gateway** (if fronted) — in `app/services/gateway`: add
   `<NAME>_GRPC_PORT` to `.env`/`.env.example` and to `internal/config.go` +
   `internal/app.go`; pass it through `api.Config`; in `internal/api/api.go`
   init the service's `client`, build `internal/api/<name>/` handlers, mount
   them on a `v1.NewGroup("/<name>")`, and close the conn in the returned
   `Close`. Compose: add the `<NAME>_GRPC_PORT: <name>:50051` environment
   override and a `depends_on` entry on the gateway.

## 4. Verify

1. `make <name>/check` then `make check-all` — everything compiles.
2. `make <name>/lint` and `make <name>/test`.
3. `docker compose -f infra/compose/docker-compose.yml config -q`.
4. Runtime smoke: `make start`, then `make <name>/serve`; for a DB service run
   `make <name>/migrate` first (or serve with `--migrate`). If gateway-fronted,
   `make gateway/serve` and curl one of the new routes.
5. `kustomize build infra/k8s/dev/<name>` renders without errors (if k8s
   manifests were added).
