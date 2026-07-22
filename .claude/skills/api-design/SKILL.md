---
name: api-design
description: Design or review a service API — gRPC/protobuf service definitions (the workspace default) or REST endpoints on the gateway. Use when adding RPCs, designing a new proto service, reviewing API changes for breaking changes, or shaping gateway REST routes.
---

# API design

Services expose **gRPC** defined in a shared `proto/` module and generated with
`buf`; REST exists only at the gateway edge. Design the proto contract first —
the REST shape derives from it.

## gRPC / protobuf conventions

**Package & layout**

- Package `<service>.v1`; files live at `proto/<service>/v1/`.
- One `<aggregate>_service.proto` per aggregate (e.g. `project_service.proto`,
  `commitment_service.proto`) — a service groups the RPCs of one domain area,
  not one per entity.
- Shared messages (entities, enums) go in `<service>/v1/shared_message.proto`
  and are imported by the service files.
- Versioning is by package: breaking changes mean a new `v2` package, never
  in-place edits to `v1`.

**RPC naming — standard CRUD verbs**

Every entity managed by a service gets the same five RPCs:

```proto
service CommitmentService {
  rpc GetGoal(GetGoalRequest)       returns (Goal);
  rpc ListGoals(ListGoalsRequest)   returns (ListGoalsResponse);
  rpc CreateGoal(CreateGoalRequest) returns (Goal);
  rpc UpdateGoal(UpdateGoalRequest) returns (Goal);
  rpc DeleteGoal(DeleteGoalRequest) returns (google.protobuf.Empty);
}
```

- Request messages are always `<Rpc>Request`, even when they hold a single id —
  it keeps the contract evolvable without breaking callers.
- `Get`/`Create`/`Update` return the entity message itself; `List` returns a
  `List<Entity>sResponse` wrapping a `repeated` field; `Delete` returns
  `google.protobuf.Empty`.
- A `List` with no parameters may take `google.protobuf.Empty`, but prefer an
  (initially empty) `ListXsRequest` if filters are plausible later.

**Message field rules**

- `Create` requests: required fields plain, optional fields `optional`.
- `Update` requests: `string id` first, **every other field `optional`** — the
  handler maps absent fields to NULL and the update procedure's COALESCE keeps
  current values (see `rules/sql.md`).
- `List` filters: `optional` fields on the request, mapped to `sqlc.narg()` —
  one RPC, one query, filters disabled by NULL.
- IDs are `string` (UUID text form) on the wire.
- Enums live in `shared_message.proto` next to the entity; zero value is
  `<ENUM>_UNSPECIFIED`.
- Never renumber or change the type of an existing field; never reuse a removed
  field's number — reserve it.

**Handler discipline**

- `context.Context` flows from the interceptor through the UseCase to the DB —
  never `context.Background()` inside a handler.
- Map errors to gRPC status codes at the handler boundary (`NotFound`,
  `InvalidArgument`, `Internal`); never leak raw SQL/driver errors to clients.

**Workflow**: edit protos → `buf generate` → implement handler → register the
service on the `core/grpc` server.

## REST (gateway edge)

When exposing an RPC through the gateway, follow
[references/rest-rules.md](references/rest-rules.md). The essentials:

- Resources are plural kebab-case nouns; HTTP verbs carry the action:
  `GET /goals`, `POST /goals`, `PATCH /goals/{id}`, `DELETE /goals/{id}`.
- Nesting max two levels (`/projects/{id}/tasks`); deeper relationships flatten
  to query filters (`/tasks?project={id}`).
- Status codes: 200/201/204 for success (201 + body on create, 204 on delete),
  400 validation, 401/403 auth, 404 not found, 409 conflict — mirror them from
  the gRPC status.
- List endpoints always paginate; filters are query params matching the RPC's
  optional filter fields.

## Reviewing an API change

1. **Breaking-change scan** (proto): removed/renumbered/retyped fields, removed
   RPCs, changed request/response types, enum value removals → any of these in
   `v1` is a blocker; require a `v2` package or an additive alternative.
2. **Consistency scan**: naming matches the CRUD pattern above; update/filter
   fields are `optional`; entity messages live in `shared_message.proto`.
3. **Contract hygiene**: new fields added at the end with fresh numbers;
   reserved numbers for anything deleted.
