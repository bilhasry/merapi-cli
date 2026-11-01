# Design: Authentication documentation in merapi

Status: implemented · Scope: document auth (OpenAPI `securitySchemes` + per-
operation `security`), mock enforcement, sample injection. Short by design.

## Why

Auth is part of an API's contract: which schemes exist (API key, bearer JWT,
OAuth2), which operations require them, and which status codes answer missing
or invalid credentials. Today merapi can *send* auth in test scenarios
(`Authorization: "Bearer {{secret:TOKEN}}"` in headers) but cannot *document*
it — so the spec, the mock, the samples and agents are all auth-blind. This
closes that gap.

## Model (OpenAPI 3.1, as-is — no invented syntax)

```yaml
components:
  securitySchemes:
    BearerAuth:            # http bearer (JWT)
      type: http
      scheme: bearer
      bearerFormat: JWT
    ApiKeyAuth:            # apiKey in a header
      type: apiKey
      in: header           # header | query | cookie
      name: X-API-Key
    OAuth2:                # oauth2 (flows kept opaque; agents can fill them)
      type: oauth2
      flows: { ... }
paths:
  /pets:
    get:
      security:
        - BearerAuth: []   # OR semantics between list items
      ...
```

- Supported scheme types: `apiKey` (header/query/cookie), `http`
  (bearer/basic/digest/…), `oauth2` and `openIdConnect` (flows/URL kept as an
  opaque map — merapi never interprets them).
- Per-operation `security` is a list of requirement objects (OR between
  items, AND within one item's keys). `security: []` = explicitly public
  (overrides a document-level default).
- Document-level `security` (applies to every operation without its own) is
  supported read/write.

## Phase 1 (this change)

| Piece | Behavior |
|---|---|
| `internal/spec` | `SecurityScheme` type + CRUD on `components/securitySchemes` (`ListSecuritySchemes`, `GetSecurityScheme`, `AddSecurityScheme`, `DeleteSecurityScheme`); `Endpoint.Security` (`[]map[string][]string`) decoded/encoded in `GetEndpoint`/`AddEndpoint`/`UpdateEndpoint`; `Document.Security` for the document-level default; delete refuses while an operation references the scheme |
| `merapi security` | `security list` (schemes + who uses them), `security add <name> --type apiKey --in header --name X-API-Key` / `--type http --scheme bearer [--bearer-format JWT]` / `--type oauth2 --flows-file flows.yaml`, `security delete <name>`; `endpoint add/update --security Scheme[:scope1,scope2]` (repeatable, OR), `--security ""` = public |
| Validator | error when an operation's `security` references an undefined scheme (libOpenAPI already errors on malformed schemes; this catches dangling references) |
| Mock | operations with effective `security` are **enforced**: missing/empty credential → the documented 401/403 (lowest documented 4xx auth code; falls back to a synthetic `401 {"error": "unauthorized"}` when none is documented); valid-shaped credential → normal response. `X-Mock-Status` still overrides the success path. Checks are *shape-only* (header present, `Bearer <non-empty>`) — the mock never validates tokens |
| Samples | secured operations get the auth header injected in cURL/Go/Python/JavaScript with a `{{secret:NAME}}`-style placeholder, so generated docs/tests are copy-paste correct |
| MCP | `security_list`, `security_add`, `security_delete` (names only; flows opaque); `endpoint_add`/`endpoint_update` accept `security` |

Deliberately excluded (phase 2 candidates): OAuth2 flow validation, scopes in
CLI beyond `:scope1,scope2` syntax, mock token *verification* (compare against
a secret), OpenID Connect discovery, mTLS (`mutualTLS`) — though hand-written
`mutualTLS` schemes pass through untouched.

## Boundaries

- Purely additive: projects without `securitySchemes` behave exactly as
  before; REST and GraphQL pipelines are unaffected.
- Secrets rules unchanged: samples emit placeholders, never values; MCP never
  returns credentials; the mock checks presence/shape only.
- Comment-preserving edits: schemes are edited under `components/` like
  schemas; operation `security` is set wholesale per operation.
