# mokapay

Go microservices stack for mokapay: gRPC services behind an HTTP gateway,
protobuf contracts via buf, MongoDB/PostgreSQL storage, deployable to kind/k8s.

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
