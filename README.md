# template

Monorepo template for a Go microservices stack: gRPC services behind an HTTP
gateway, protobuf contracts via buf, MongoDB/PostgreSQL storage, deployable to
kind/k8s. Clone it, rename `template` to your project name, and start adding
services.

## Layout

```
proto/         # protobuf contracts (buf); generated code in proto/codebase/
core/          # shared Go library packages (db, http, logger, otel, config)
app/services/  # Go services (gateway, …)
app/tooling/   # workspace tooling binaries (logfmt)
infra/         # compose (local dev) + k8s kustomize manifests
docs/          # design docs & plans (git-ignored)
```

## Everyday commands

Everything runs through Make — a root Makefile for global commands, one
Makefile per app, delegated as `make <service>/<target>`:

```bash
make start                     # local infra via compose (mongodb, postgres, redis)
make gateway/serve             # run a service locally with pretty logs
make gateway/check             # compile-check a service
make check-all                 # compile-check all Go services
make build                     # build all docker images
make proto                     # regenerate protobuf code (buf)
make kind-up                   # local k8s cluster
```

See `CLAUDE.md` for the full command reference and workspace conventions.

## Starting a new project from this template

In Claude Code, run `/rename-project` (renames the `template.app` module and
every `template` reference to your project name), then `/add-service` to
scaffold your first service. Manual equivalent:

1. Rename the Go module: replace `template.app` with `<yourproject>.app` in
   `go.mod`, every `.go` import, and `buf.gen.yaml`; run `make proto` and
   `make tidy`.
2. Replace the project name `template` in `vars.env` (kind cluster, image
   names), `infra/compose/docker-compose.yml` (compose project, networks,
   dev credentials), and `buf.yaml`.
3. Add your first service under `app/services/` (mirror the gateway layout and
   Makefile), register it in the root Makefile (`GO_SERVICES`, a delegation
   rule, `build`) and in compose/k8s.
