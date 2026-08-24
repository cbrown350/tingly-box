# apierr — shared API error responses

`internal/apierr` owns the structured JSON error shape used by the
management API and the protocol endpoints:

```json
{"error": {"message": "...", "type": "invalid_request_error", "code": "optional"}}
```

It depends only on gin, so any layer — server modules, `internal/server`
root, middleware, protocol handlers — can use it without import cycles.
It started as `internal/server/module/apierr` (extracted from the team
module's `sendError`) and was promoted to `internal/apierr` when the
middleware and protocol layers picked it up.

## API

- `ErrorResponse` / `ErrorDetail` — the wire structs. `internal/protocol`
  and `internal/protocolserver` re-export them as type aliases so their
  many construction sites (and `server_types.go`) keep compiling and all
  three layers share one definition.
- `Type*` constants — the common `type` values (`invalid_request_error`,
  `validation_error`, `not_found_error`, `conflict_error`,
  `internal_error`, `api_error`). Domain-specific values (`probe_error`,
  `extraction_error`, `protocol_error`, …) stay as literals at their
  single call site.
- All helpers are named for what they do to the gin context — they *send*
  a response (or *abort* the chain), they never build and return an error
  value:
  - `Send` / `SendMsg` — generic status + type.
  - `SendBadRequest[Msg]`, `SendRequired`, `SendNotFound[Msg]`,
    `SendInternal[Msg]` — the recurring status/type pairings.
  - `SendStoreError` — maps db-store errors by the stores' message
    conventions: "not found" → 404, "already exists" / "unique" /
    "disabled" → 409, else the caller's default. Used by team and sharing
    handlers instead of per-module substring inference.
  - `Abort` — send + register on the gin context for logging middleware +
    `c.Abort()`; used by auth middleware rejections.

## Scope: two error shapes exist

Only the structured shape above goes through apierr. A number of older
module handlers (imbot, notify, usage, debug, rule, trace) return a flat
`{"error": "message"}` body; that is a different wire contract the
frontend already consumes, so those were deliberately **not** migrated —
converting them is an API-breaking change that needs a coordinated
frontend pass, not a refactor.

In-stream SSE error frames (`internal/protocol/stream`, anthropic-style
`{"type":"error", ...}` events) are a separate protocol-level format and
also out of scope.

## Envelope embedding

probe and onboarding wrap errors in success envelopes
(`{"success": false, "error": {...}}`). They embed `*apierr.ErrorDetail`
directly (onboarding aliases it as `ErrorDetail` for its swagger model),
which merged the former duplicate `errorDetail` / `ErrorDetail` openapi
schemas into one.
