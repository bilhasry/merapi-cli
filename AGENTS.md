# AGENTS.md — working with merapi as an AI agent

This file is for AI agents (and humans automating merapi). It explains how to
drive the `merapi` CLI safely and effectively: how to connect over MCP, which
commands to use for which task, the file formats on disk, and the safety rules
(secrets especially) you must never break.

There are two ways to interact with a project:

1. **MCP server (preferred)** — run `merapi mcp` inside a project directory and
   call the typed tools below. Every mutation is persisted immediately and
   preserves hand-written YAML comments.
2. **Direct CLI** — run `merapi ...` commands yourself. Same capabilities;
   use `--json` for machine-readable output and check exit codes.

Both operate on the same git-friendly files, so you can also *read* and edit
`openapi.yaml`, `tests/*.yaml` and `environments/*.yaml` directly — but prefer
the CLI/MCP tools for mutations, because they keep the document valid and
comment-preserving.

## Quick orientation

```bash
merapi version                 # -> merapi v0.1.0
merapi --help                  # all commands
merapi -C <dir> <cmd>          # target a project from anywhere
merapi --json <cmd>            # machine-readable output (use this!)
```

A project is identified by `merapi.yaml` (searched upward from cwd, like git):

```
my-api/
├── merapi.yaml            # project config (spec path, tests dir, default env)
├── openapi.yaml           # OpenAPI 3.1 document (comment-friendly YAML)
├── schema.graphql         # optional: GraphQL SDL (served by `merapi mock`)
├── tests/                 # test scenarios (*.yaml)
├── environments/
│   ├── dev.yaml           # base_url + plain variables (committed)
│   └── staging.yaml
└── .merapi/               # gitignored: active env + encrypted secrets
    ├── state.yaml
    └── secrets.staging.enc
```

## MCP setup

```bash
merapi mcp          # stdio transport; run inside a project directory
```

Client config example:

```json
{ "mcpServers": { "merapi": { "command": "merapi", "args": ["mcp"] } } }
```

### Tools (35, all typed; errors come back as readable messages)

| Group | Tools | Notes |
|---|---|---|
| spec | `spec_summary`, `spec_validate` | validate after every edit |
| endpoints | `endpoint_list`, `endpoint_get`, `endpoint_add`, `endpoint_update`, `endpoint_delete` | CRUD on operations |
| schemas | `schema_list`, `schema_get`, `schema_add`, `schema_delete` | CRUD on `components/schemas` |
| security | `security_list`, `security_add`, `security_delete` | CRUD on `components/securitySchemes` (names only; flows opaque) |
| samples | `doc_samples` | ready-to-paste cURL/Go/Python/JavaScript |
| graphql | `gql_validate`, `gql_show`, `gql_query` | SDL validation, type/operation listing, deterministic example responses |
| tests | `test_list`, `test_get`, `test_create`, `test_scaffold`, `test_delete`, `test_run` | scenario management + execution |
| environments | `env_list`, `env_get`, `env_create`, `env_use`, `env_delete` | |
| variables | `var_list`, `var_set`, `var_unset` | plain values, committed |
| secrets | `secret_list`, `secret_set`, `secret_unset` | **names only — values are never returned** |

### Resources

- `merapi://project` — human-readable project overview
- `merapi://openapi` — the raw OpenAPI document
- `merapi://tests/{file}` — one test scenario

All mutations are guarded by a global lock (safe under concurrent tool calls).
After any edit, run `spec_validate` — the server instructs agents to do this,
and it is your safety net.

## Integrating with coding agents

Both Claude Code and Codex read `AGENTS.md` automatically when it sits in the
project (or repository root), so this file *is* the onboarding doc — keep it
updated when tools or workflows change.

> Secrets note: point the MCP server at a project only if `MERAPI_SECRETS_KEY`
> is available in that context. Prefer session-scoped env injection over
> hardcoding the key into committed config (`~/.claude.json`, `.mcp.json`,
> `~/.codex/config.toml` are often synced or committed).

### Claude Code

Register the MCP server (user scope by default; add `--scope project` to write
a committable `.mcp.json` instead):

```bash
claude mcp add merapi -- merapi mcp                # user scope
claude mcp add merapi --scope project -- merapi mcp    # writes .mcp.json
claude mcp add merapi --env MERAPI_SECRETS_KEY -- merapi mcp   # pass the key (careful: scope!)
claude mcp list                                    # verify it connects
```

`.mcp.json` (project scope, shareable with the team):

```json
{
  "mcpServers": {
    "merapi": { "type": "stdio", "command": "merapi", "args": ["mcp"] }
  }
}
```

Recommended `settings.json` permissions so merapi commands run without
per-invocation prompts, while still blocking secret exfiltration:

```json
{
  "permissions": {
    "allow": [
      "Bash(merapi *)",
      "mcp__merapi"
    ],
    "deny": [
      "Bash(merapi secret get*)"
    ]
  }
}
```

Also consider a `CLAUDE.md` that simply points here, so both entry points stay
in sync:

```markdown
See AGENTS.md for how to work with the merapi CLI and MCP tools.
```

### Codex

Register the MCP server via CLI (writes `~/.codex/config.toml`):

```bash
codex mcp add merapi -- merapi mcp
codex mcp list                                    # verify
```

Or edit `~/.codex/config.toml` (or a repo-local `.codex/config.toml`) by hand:

```toml
[mcp_servers.merapi]
command = "merapi"
args = ["mcp"]
# env = { MERAPI_SECRETS_KEY = "..." }   # avoid committing; prefer shell export
```

Codex reads `AGENTS.md` from the working directory upward — this file is
already picked up. To make the loop fully autonomous, allow merapi in your
sandbox/approval config (e.g. `~/.codex/config.toml`):

```toml
approval_policy = "never"          # or "on-request"
sandbox_mode = "workspace-write"
trusted_commands = ["merapi *"]    # if your setup supports command allowlists
```

For unattended/CI runs, prefer `codex exec` with the merapi project as cwd and
`MERAPI_SECRETS_KEY` injected via the job's secret store.

### Verifying an integration (smoke test)

From a project directory, both agents should be able to: list tools (35, incl.
`doc_samples`, `gql_validate`, `security_list`), read `merapi://project`, add a throwaway endpoint, validate, and
delete it again. If tool calls fail with "not found in the document", the agent
is likely using a stale cwd — the MCP server resolves the project from the
directory it was started in (`-C <dir>` semantics do not apply over MCP; restart
the server in the right project instead).

## Common workflows

### Document an API

```bash
merapi init --title "My API" --server https://api.example.com
# schemas first, so operations can reference them
merapi schema add Pet --property id:integer --property name:string --required id,name
merapi schema add Error --property code:integer --property message:string
# operations
merapi endpoint add GET /pets --summary "List pets" --response 200:OK:Pet
merapi endpoint add POST /pets --summary "Create a pet" --request-body Pet --response 201:Created:Pet --response 400:BadRequest:Error
merapi endpoint add GET /pets/{id} --param id:path:required --summary "Get a pet" --response 200:OK:Pet --response 404:NotFound:Error
# auth (see below)
merapi security add BearerAuth --type http --scheme bearer --bearer-format JWT
merapi endpoint add GET /pets --summary "List pets" --security BearerAuth --response 200:OK:Pet
merapi validate            # exit 1 on errors; --warnings-as-errors for CI
```

Rich operations (many parameters, inline schemas, examples) can be fed from a
file: `merapi endpoint add -f endpoint.yaml` or piped via `-f -`.

### Document authentication

```bash
merapi security add BearerAuth --type http --scheme bearer --bearer-format JWT
merapi security add ApiKeyAuth --type apiKey --in header --name X-API-Key
merapi security add OAuth2 --type oauth2 --flows-file flows.yaml
merapi endpoint add GET /admin --security BearerAuth --response 200:OK --response 401:Unauthorized
merapi endpoint add GET /health --public           # security: [] — explicitly public
merapi security global --set BearerAuth            # document-level default (override with --public)
merapi security list                               # schemes + operations using each
```

- `--security SCHEME[:SCOPE1,SCOPE2]` on `endpoint add|update` is repeatable
  (OR between items; AND within one item's keys, per OpenAPI). `endpoint_update`
  replaces the whole list when `security` is passed; `--public` clears it.
- Referencing an undefined scheme is a **validation error** — run `merapi validate`
  (or `spec_validate`) after attaching auth.
- The mock enforces effective security (documented 401/403, else a synthetic 401;
  shape-only — tokens are never validated), and `doc_samples` injects
  `{{secret:NAME}}` credential placeholders (`BEARER_AUTH`, `X_API_KEY`).
- MCP: `security_list` / `security_add` / `security_delete`; `endpoint_add` and
  `endpoint_update` accept a `security` field.
- Design: [docs/design/auth.md](docs/design/auth.md).

Documentation tips that the validator also enforces:
- every operation needs `operationId`, `summary`, and documented responses
- request/response schemas should be `$ref`s into `components/schemas`, not
  anonymous blobs — refs make samples, mocks and tests all work better
- model **create/update bodies separately** from the resource (e.g. `PetCreate`
  without the server-assigned `id`) so `required` lists are truthful

### Document a GraphQL API

A project may document REST, GraphQL, or both. GraphQL lives in an SDL file at
the project root (`schema.graphql`, or `graphql: <file>` in `merapi.yaml`):

```bash
merapi gql validate           # exit 1 on SDL errors (CI-friendly)
merapi gql show               # types, queries, mutations with args (--json for agents)
merapi mock                   # also serves POST /graphql from the schema
```

- `merapi mock` answers `POST /graphql` with a deterministic example response
  derived from the SDL: it parses `{query, variables, operationName}` and
  returns the standard GraphQL envelope. Invalid queries produce
  `{"errors": […]}` with HTTP 200 (GraphQL convention); `X-Mock-Status` still
  overrides the status for error-path exploration.
- Design and scope: [docs/design/graphql.md](docs/design/graphql.md).
- Subscriptions and fragment spreads are not served by the mock yet (phase 2);
  inline fragments are merged.

### Test an API

```bash
merapi test scaffold --all            # generate starters from the docs
merapi test list
merapi test run [--filter x] [--fail-fast] [--timeout 30s] [--json]
```

Scenario format (one file per scenario, ordered steps, captures chain):

```yaml
name: pet lifecycle
steps:
  - name: create a pet
    request:
      method: POST
      path: /pets
      headers:
        Authorization: "Bearer {{secret:API_TOKEN}}"
      json:
        name: "{{pet_name}}"
    expect:
      status: 201
      json:
        "$.id": "@exists"
        "$.name": "{{pet_name}}"
    capture:
      pet_id: "$.id"          # available to later steps
  - name: read it back
    request:
      method: GET
      path: /pets/{{pet_id}}
    expect:
      status: 200
      max_duration_ms: 1500
```

Templates (resolved in paths, headers, query, bodies **and assertion values**):
- `{{base_url}}` — the environment's base URL
- `{{name}}` — environment variable, overridden by captures
- `{{secret:NAME}}` — decrypted secret (requires `MERAPI_SECRETS_KEY`)

Assertions: `status`, `status_in`, `headers`, `body_contains`,
`max_duration_ms`, and `json` with a JSONPath subset (`$.a.b[0]`, `$.items[*]`
in captures). Expected values: literals, numbers, nested objects/arrays (subset
match), or the markers `@exists`, `@not_exists`, `@null`.

`test run` exits non-zero on failure — safe to gate CI on.

### Run tests without a backend (mock server)

```bash
merapi mock                          # http://127.0.0.1:9090
merapi mock --addr 0.0.0.0:8080 --delay 250
merapi mock --tunnel auto            # also expose to the internet
```

`merapi mock` serves every documented operation, which makes it the fastest way
to verify your docs and scenarios end-to-end:

- answers with the documented success response (lowest 2xx); body is the
  response `example` when set, else generated from the schema
- **stateful**: `POST` assigns an `id` and stores the resource (create→read-back
  works), `DELETE` evicts it
- **validates** write bodies against schema `required` lists — empty/missing
  fields get the documented 4xx with an injected message
- `X-Mock-Status: <code>` returns any other documented response (error paths!)
- `GET /` lists all mocked operations; wrong methods → `405` + `Allow` header
- **auth**: operations with effective `security` reject missing credentials with
  the documented 401/403 (synthetic `401 {"error": …}` when none is documented);
  checks are shape-only, token values are never validated
- **GraphQL**: when an SDL schema is present (`schema.graphql` or `graphql:` in
  `merapi.yaml`), `POST /graphql` is served too — deterministic examples from
  the schema, GraphQL-convention error envelopes (HTTP 200 + `errors`)
- `--tunnel auto|ngrok|cloudflared` exposes the mock to the internet through
  the matching binary (must be in `PATH`); the public HTTPS URL is printed when
  up. Use this for webhook testing or remote collaborators — and remember
  **anyone with the URL can hit the mock**, so stop it when done. If the tunnel
  fails to start, the mock still runs locally only.

Typical loop: `merapi mock &` → point an env's `base_url` at it →
`merapi --env mock test run`. A failure against the mock usually means a
**documentation bug** (wrong response shape, missing required fields) — fix the
spec, not the mock.

### Environments, variables, secrets

```bash
merapi env add staging --base-url https://api.staging.example.com
merapi env use staging
merapi var set tenant acme
merapi secret set API_TOKEN            # hidden prompt; or pipe: echo -n "$T" | merapi secret set API_TOKEN
merapi env show                        # values stay hidden
```

### Docs & samples

```bash
merapi doc markdown -o API.md          # Markdown reference incl. code samples
merapi doc preview                     # live Swagger UI on :8085
merapi doc export -o dist/index.html   # standalone HTML
merapi doc samples POST /pets --lang curl,go
```

Samples use the active environment's base URL (override with `--base-url`).
Languages: `curl`, `go`, `python`, `javascript`.

## Safety rules (never violate)

1. **Secrets are read-only for you.** `secret_get` exists for humans; MCP tools
   never return secret values. Never print, log, echo or write secret values
   into test files, docs, commit messages or your responses — reference them as
   `{{secret:NAME}}` instead.
2. `MERAPI_SECRETS_KEY` unlocks encrypted secrets (AES-256-GCM, scrypt-derived).
   Never invent or hardcode one; if a command needs it and it's missing, ask the
   user to export it.
3. **Validate after every document edit** (`merapi validate` / `spec_validate`).
4. Prefer CLI/MCP mutations over raw YAML edits (comment preservation +
   structural integrity). When editing YAML directly (e.g. adding examples),
   re-run `merapi validate` afterwards.
5. `test run` exit codes are meaningful — don't swallow failures.
6. Check `--json` output and exit codes, not stdout prose, when scripting.

## Developing merapi itself

Go ≥ 1.24, module `github.com/bilhasry/merapi`.

```bash
make build     # -> bin/merapi (version injected via ldflags)
make test      # go test ./...
make vet
make fmt       # gofmt -w cmd internal
make demo      # builds + exercises the CLI in ./demo
make clean
```

Layout:
- `cmd/merapi` — main
- `internal/spec` — comment-preserving OpenAPI editing + validation (libOpenAPI)
- `internal/runner` — scenario execution
- `internal/envs` — environments + encrypted secrets
- `internal/templ` — strict template resolution
- `internal/docs` — Swagger UI/Markdown/code samples
- `internal/mock` — deterministic mock server
- `internal/tunnel` — ngrok/cloudflared integration for public mock URLs
- `internal/gql` — GraphQL SDL parsing, validation and example generation (gqlparser)
- `internal/mcpserver` — MCP tools/resources
- `internal/cli` — cobra commands

Conventions: standard Go layout, `gofmt`-clean, tests live next to the code
(`*_test.go`); MCP tools are registered in `internal/mcpserver/server.go` and
must be added to the expected-tools list in `server_test.go`. Run
`go vet ./... && go test ./... && gofmt -l .` before considering any change done.
