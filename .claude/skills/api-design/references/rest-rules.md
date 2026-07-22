# REST design rules

For gateway-edge REST endpoints. Distilled to the rules that matter; the gRPC
contract remains the source of truth for payload shapes.

## Resources, not actions

Nouns in the URL, verbs from HTTP:

```
GET    /goals            # list
GET    /goals/{id}       # fetch one
POST   /goals            # create → 201 + created body
PATCH  /goals/{id}       # partial update → 200 + updated body
DELETE /goals/{id}       # delete → 204, no body
```

Never `POST /createGoal`, `POST /goals/{id}/delete`, etc. The one exception:
genuine domain *operations* that aren't CRUD (e.g. `POST /goals/{id}:complete`
or `POST /goals/{id}/complete`) — use them sparingly and document them.

## Naming

- Plural kebab-case collections: `/user-profiles`, `/schedule-blocks`.
- Path params are ids only; everything else is a query param.
- Nesting expresses ownership, max two levels: `/projects/{id}/tasks` is fine;
  anything deeper flattens to filters: `/tasks?project={id}`.

## Methods & idempotency

| Method | Use | Idempotent | Body |
|---|---|---|---|
| GET | read | yes | no |
| POST | create / non-idempotent operation | no | yes |
| PUT | full replace (rare here) | yes | yes |
| PATCH | partial update — matches optional-field Update RPCs | no* | yes |
| DELETE | delete (soft, per workspace rules) | yes | no |

*PATCH with absent-means-unchanged semantics is effectively idempotent — keep
it that way; never make PATCH increment or toggle.

## Status codes

- `200` success with body, `201` created (body + `Location`), `204` success
  without body (deletes).
- `400` malformed/invalid input (validation detail in body), `401`
  unauthenticated, `403` unauthorized, `404` not found (also for
  soft-deleted rows), `409` conflict/duplicate, `422` semantically invalid.
- `500` only for genuine server faults — never for client errors, and never
  with internal error detail in the body.

Mirror from gRPC: `InvalidArgument→400`, `Unauthenticated→401`,
`PermissionDenied→403`, `NotFound→404`, `AlreadyExists→409`, `Internal→500`.

## Errors

One error envelope everywhere:

```json
{ "error": { "code": "GOAL_NOT_FOUND", "message": "goal not found" } }
```

Stable machine-readable `code`, human `message`, optional `details` for field
validation. Never a bare string, never a raw stack/SQL error.

## Lists

- Always paginated — `?limit=` (with a server-side max) and a cursor or offset;
  return the page metadata in the body.
- Filters are query params named after the RPC's optional filter fields:
  `/goals?type=weekly`.
- Stable default ordering (`created_at DESC` per workspace convention).

## Versioning & evolution

- Version in the path prefix, matching the proto package: `/v1/goals`.
- Additive changes only within a version: new optional fields, new endpoints.
  Renaming/removing fields, changing types, or changing semantics → `/v2`.
- Unknown fields in requests are ignored, never errors — clients on older
  shapes must keep working.
