# Universal review checklist — all languages

Apply to every review, alongside the relevant language checklist.

## Security

- Flag any string interpolation or concatenation used to build SQL, shell, or
  LDAP queries — require parameterized queries or a safe API.
- Flag hardcoded credentials, API keys, tokens, or secrets anywhere in source —
  require environment variables or a secrets manager.
- Flag user-controlled input passed to file system, process execution, or URL
  redirect APIs without validation.
- Flag overly broad CORS or CSP policies.

## Async / concurrency

- Flag shared mutable state accessed from multiple threads/goroutines/tasks
  without synchronization.
- Flag fire-and-forget async operations with no error handling path.
- Flag timeouts missing on any network or I/O call.
- Flag unbounded queues or worker pools with no backpressure mechanism.

## Resource management

- Flag any resource (file, socket, DB connection, HTTP connection) acquired
  without a guaranteed release path.
- Flag connection pools not returned to the pool on all code paths (including
  error paths).
- Flag unbounded collections that grow without eviction — potential memory leak.
- Flag resources held open longer than the operation they serve.

## Error handling

- Flag empty catch/except blocks — swallowed errors hide bugs silently.
- Flag catching the broadest possible error type where a specific type is
  appropriate.
- Flag exceptions used for normal control flow (signaling "not found", etc.) —
  use return values.
- Flag error context lost when re-throwing/re-returning — always wrap with the
  original cause.

## Performance

- Flag N+1 query patterns — loading a collection then querying for each item
  individually.
- Flag unbounded queries or API calls with no pagination or limit.
- Flag synchronous I/O on a thread or event loop that serves concurrent requests.
- Flag large objects serialized/deserialized repeatedly when they could be cached.
- Flag string concatenation in tight loops — use a builder or join.
