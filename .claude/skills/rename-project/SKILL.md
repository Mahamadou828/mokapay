---
name: rename-project
description: Rename the template to a real project — replace the template.app Go module and every `template` project reference (vars.env, compose, buf, k8s, docs) with the new project name, then regenerate and verify. Use right after cloning this template, or when asked to rename the project/module.
---

# Rename the project

Turn this cloned template into project `<name>`: Go module `template.app` →
`<name>.app`, project name `template` → `<name>` everywhere it appears.

Get `<name>` from the user if not given. It must be short, lowercase,
`[a-z][a-z0-9-]*` (it becomes a compose project, docker image path, kind
cluster name, and — with `.app` — the Go module path; module paths must not
end in `-`).

## 1. Global module rename (safe blanket replace)

The string `template.app` is distinctive — replace it repo-wide:

```bash
grep -rl --exclude-dir=.git --exclude-dir=.idea --exclude-dir=node_modules \
  'template\.app' . | xargs sed -i 's|template\.app|<name>.app|g'
```

This covers in one pass: `go.mod` (module line), every `.go` import,
`buf.gen.yaml` (`go_package_prefix`), the gateway Dockerfile (`-ldflags -X`
path), the compose `<name>.app` network (key and references), core package
READMEs, and code samples in `CLAUDE.md`/`README.md`/`.claude/` docs.

## 2. Targeted `template` renames (never blanket-sed bare `template`)

The bare word `template` appears in legitimate prose — replace only these
specific values:

- **`vars.env`** — `KIND_CLUSTER=template`, `BASE_IMAGE_NAME=localhost/template`.
- **`infra/compose/docker-compose.yml`** — `name: template`; the
  `template.infrastructure` network (key + every service reference); the
  local-dev credentials (`POSTGRES_USER/PASSWORD/DB: template`,
  `MONGO_INITDB_ROOT_USERNAME/PASSWORD: template`, `pg_isready -U template`).
- **`buf.yaml`** — module name `buf.build/template/api` → `buf.build/<name>/api`.
- **`infra/k8s/dev/*/kustomization.yaml`** — image `localhost/template/<svc>`.
- **`README.md` + `CLAUDE.md`** — title and prose: the repo is no longer a
  template, so also rewrite the intro sentences ("monorepo template …",
  "Clone it, rename …") to describe the actual project, and drop the
  README's "Starting a new project from this template" section.

If any service `.env` files reference the old dev credentials
(`DB_USER=template` etc.), update them to match compose.

## 3. Regenerate & tidy

Never hand-edit files under `proto/codebase/` — regenerate them:

```bash
make proto     # picks up the new go_package_prefix
make tidy      # go mod tidy under the new module name
```

## 4. Verify

1. `go build ./... && go vet ./...` at the repo root (covers core/, tooling,
   and generated proto code the per-service builds miss).
2. `make check-all` and `make test-all`.
3. `docker compose -f infra/compose/docker-compose.yml config -q`.
4. Final sweep — must return nothing:
   ```bash
   grep -rn --exclude-dir=.git --exclude-dir=.idea --exclude-dir=node_modules \
     -e 'template\.app' -e 'localhost/template' -e 'buf\.build/template' .
   ```
   Then review the remaining bare-word hits of `template` (skills/rules prose
   legitimately keeps some, e.g. this file) and confirm none are config values.
5. Optional runtime smoke: `make start`, `make gateway/serve`,
   `curl localhost:8080/v1/readiness`.

## Note on stateful local infra

If the old compose project already ran on this machine, its containers/volumes
keep the old project name and credentials. After renaming, start fresh:
`docker compose -p template down -v` (old name) before `make start`.
