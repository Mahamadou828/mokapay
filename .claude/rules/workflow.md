# Workflow

How to verify work in this workspace.

---

## Verifying Go changes: use the service's make targets

When you change a Go service, verify **that service** with its Makefile targets,
run from the repo root:

```bash
make gateway/check   # compile-checks every package (go build ./...)
make gateway/lint    # go vet + go fmt
make gateway/test    # go test ./...
```

Substitute the service you are working on (`gateway`, …). Each target
delegates to `app/services/<service>/Makefile`, so
`make -C app/services/gateway check` works too. You do **not** need to
build the whole workspace — checking the one service you touched is enough
(`make check-all` exists when you do want all).

`make <service>/build` produces the runnable binary in
`app/services/<service>/bin/` — use `check` for verification (it compiles every
package, not just `main`).

For a frontend (once the project adds one): `make web/build`,
`make web/typecheck`, `make web/lint`, `make web/test`.

### Unrelated failures: report, don't fix

If the service's `check`, `lint`, or `test` fails on an error **unrelated to
your change** (something that was already broken, a half-finished refactor
elsewhere), do not fix it and do not work around it. Stop and notify the user:
name the file and the error, and say it predates your change. The user decides
what to do with it.
