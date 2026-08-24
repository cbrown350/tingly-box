# apierr — API error semantics

Error handling is split by API surface, with opposite philosophies:

- **Protocol surface** (`/v1`, `/tingly/...`, `/virtual/...` — clients are
  SDKs and agent CLIs): **relay faithfully**. Status is always the
  upstream's; the body is the upstream's own bytes whenever possible.
- **Management surface** (`/api/v1` — client is our frontend):
  **normalize and sanitize**. One taxonomy, no internal details, machine
  keys for i18n.

## Shared packages

`internal/apierr` owns the structured JSON error shape used by the
management API and (via type aliases in `internal/protocol` and
`internal/protocolserver`) the protocol layer's gateway-shaped errors:

```json
{"error": {"message": "...", "type": "invalid_request_error", "code": "optional"}}
```

It depends only on gin. `internal/errkind` (dependency-free) holds the
sentinel error kinds — `ErrNotFound` / `ErrConflict` / `ErrInvalid` —
that storage layers attach with `errkind.Newf` / `errkind.Mark` so
handlers classify with `errors.Is` instead of message substrings
(substring matching misfires when user input is interpolated into the
message; a team literally named "not found" must not turn a 409 into a
404).

## Management surface rules

- **Taxonomy** (`apierr.Type*`): 400 `invalid_request_error` /
  `validation_error`, 401 `authentication_error`, 403 `permission_error`,
  404 `not_found_error`, 409 `conflict_error`, 500 `internal_error`
  (`api_error` only on the protocol-facing auth middleware's 500).
- **Store errors** go through `apierr.SendStoreError`: ErrInvalid → 400,
  ErrNotFound → 404, ErrConflict → 409 with the store's message (marked
  messages must be caller-safe); an *unmarked* error is internal — logged
  via the gin context, answered as a generic 500. Stores that feed this
  path must mark their errors (team and sharing-token stores do).
- **5xx never leak internals**: `apierr.SendInternalErr(c, err, publicMsg)`
  sends only the safe context string and registers the wrapped error for
  the logging middleware. There is deliberately no helper that serializes
  a raw error into a 500 body.
- **Diagnostics exemption**: probe/debug endpoints return raw upstream and
  SDK error text *as result data* (200 envelopes) — that text is the
  product ("diagnostics must traverse the real path"). The rule of thumb:
  error text as result data is kept; error text in an error response is
  sanitized.
- **`code` enum**: keep it tiny and consumer-driven (`not_found` on the
  SPA API 404 is the only current value). Never put prose in `code`.
- **i18n (future)**: clients should key on `type`/`code`; `message` is a
  fallback, not a contract.

## Protocol surface rules

- **Status is always passed through** (`protocol.UpstreamStatus`), with
  500 as the no-upstream fallback. This is also the **failover contract**:
  the priority-failover orchestrator decides retry-vs-terminal *solely*
  from the buffered response status (429/500/502/503/504 retry; anything
  else is terminal). Error writers may change bodies and headers freely,
  but changing which status a failure class maps to changes failover
  behavior — see `TestWriteUpstreamError_StatusPreservedForFailover`.
- **Upstream bodies pass through** via `protocol.WriteUpstreamError`:
  byte-for-byte when the client speaks the provider's protocol (the
  openai SDK stores only the inner error object, so the `{"error":...}`
  envelope is rebuilt), rebuilt in the client's shape — status-mapped
  type, verbatim message — on cross-protocol routes. The client's
  protocol is recorded per route by `protocol.WithClientStyle`.
- **Origin marking**: every protocol-surface error response carries
  `X-Tingly-Error: gateway | upstream`. A relayed upstream 401 ("your
  provider key") is thus distinguishable from a gateway auth rejection
  ("your gateway token"); upstream statuses are relayed unremapped on
  purpose — this is a self-hosted gateway, the upstream key is the
  user's own.
- **Gateway-origin errors**: 4xx rejections keep precise types
  (`invalid_request_error`); 5xx failures use `gateway_error`, so a
  client never mistakes a gateway fault for a provider fault.
- **SSE streams**: an error after streaming has started is emitted as a
  protocol-legal error *event* in the client's stream dialect (shape
  converted, content preserved) — raw passthrough is impossible
  mid-stream.

## Out of scope (deliberately)

Older module handlers (imbot, notify, usage, debug, rule, trace) return a
flat `{"error": "message"}` body; that is a separate wire contract the
frontend already consumes. Converting them is an API-breaking change that
needs a coordinated frontend pass, not a refactor.

## Envelope embedding

probe and onboarding wrap errors in success envelopes
(`{"success": false, "error": {...}}`) and embed `*apierr.ErrorDetail`
directly (onboarding aliases it for its swagger model), which merged the
former duplicate `errorDetail` / `ErrorDetail` openapi schemas into one.
