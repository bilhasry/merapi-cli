# merapi — API management from the terminal

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

`merapi` manages API documentation, tests and environments **from the CLI** — no web UI required. It is built for two kinds of users: humans who live in a terminal, and **AI agents** that connect through the built-in [MCP](https://modelcontextprotocol.io) server to maintain documentation and tests.

- **Documentation** — edit an OpenAPI **3.1** document (`paths`, `components/schemas`, `securitySchemes`) with comment-preserving YAML edits, validate it (libOpenAPI), export interactive Swagger UI HTML, generate Markdown, or print ready-to-paste **code samples** (cURL, Go, Python, JavaScript) that include auth credential placeholders.
- **GraphQL** — a project can also carry a GraphQL SDL schema (`schema.graphql`): `merapi gql validate|show` and the mock's `POST /graphql` endpoint serve deterministic example responses derived from the schema.
- **Tests** — scenario-based YAML test files with assertions, JSONPath matching and value captures; scaffolded straight from the documented endpoints.
- **Mock server** — `merapi mock` serves deterministic responses for **every documented endpoint** (and enforces documented auth: missing credentials get the documented 401/403), so tests (and clients) can run before the real backend exists.
- **Environments** — per-environment `base_url` + plain variables (committed) + **encrypted secrets** (AES-256-GCM, gitignored).
- **AI agents** — `merapi mcp` exposes ~35 typed MCP tools + resources, so any MCP-capable agent can document, test and validate the API safely.

## Problem statement

Working with APIs today usually means juggling several heavyweight tools that don't share state:

- **Documentation** lives in a GUI editor or a hosted platform (Swagger Editor, Stoplight, Postman cloud) — changes are awkward to review in a PR and often require an account.
- **Mocking** needs a separate server (Prism, WireMock) configured from yet another place.
- **Testing** happens in a different tool and format (Postman collections, Bruno) with its own environment/secrets story.
- **Environments and secrets** scatter across `.env` files, shell exports and cloud workspaces.

The result is slow feedback loops, configuration that doesn't merge cleanly, no single source of truth in git — and **no safe machine interface**: GUI and cloud tools expose nothing an AI agent can drive.

merapi collapses this into **one git-friendly, terminal-first tool**. The OpenAPI 3.1 document, GraphQL SDL, test scenarios, environments and secrets are plain files in your repo — diffable, reviewable, mergeable. The same static binary validates the docs, runs the tests, serves a deterministic mock (with auth enforcement), renders documentation and code samples, and exposes an MCP server so humans *and* AI agents work through identical, lock-protected operations. No server, no account, no runtime besides the binary.

## Install

```bash
make build          # -> bin/merapi
make install        # or: go install ./cmd/merapi
```

Requires Go ≥ 1.24.

## Quickstart

```bash
mkdir my-api && cd my-api
merapi init --title "My API" --server https://api.example.com

# schemas first, so operations can reference them
merapi schema add Pet --property id:integer --property name:string --required id,name
merapi schema add Error --property code:integer --property message:string

# document endpoints
merapi endpoint add GET /pets --summary "List pets" --description "Returns all pets." --response 200:OK:Pet
merapi endpoint add POST /pets --summary "Create a pet" --request-body Pet --response 201:Created:Pet
merapi endpoint add GET /pets/{id} --param id:path:required --summary "Get a pet" --response 200:OK:Pet --response 404:NotFound

merapi validate          # spec conformance + documentation-quality warnings

# environment
merapi env add staging --base-url https://api.staging.example.com
merapi env use staging
merapi var set tenant acme
export MERAPI_SECRETS_KEY="$(openssl rand -hex 32)"   # once per machine/team
merapi secret set API_TOKEN        # hidden prompt, stored encrypted

# tests generated from the docs
merapi test scaffold --all
merapi mock &                # serve every documented endpoint locally
merapi test run              # ...so tests run without a real backend

# human-readable + interactive docs
merapi doc markdown -o API.md
merapi doc preview            # http://127.0.0.1:8085 (Swagger UI, live)
merapi doc export -o dist/index.html
merapi doc samples POST /pets # ready-to-paste cURL / Go / Python / JavaScript
```

## Project layout

```
my-api/
├── merapi.yaml            # project config (spec path, tests dir, default env)
├── openapi.yaml           # OpenAPI 3.1 document (comment-friendly YAML)
├── tests/                 # test scenarios (*.yaml)
├── environments/
│   ├── dev.yaml           # base_url + plain variables (committed)
│   └── staging.yaml
└── .merapi/               # gitignored: active env + encrypted secrets
    ├── state.yaml
    └── secrets.staging.enc
```

`merapi` commands search upward for `merapi.yaml` (like git), so they work from any subdirectory. Use `-C <dir>` to target another project.

## Command reference

| Command | Purpose |
|---|---|
| `merapi init [dir]` | scaffold a project |
| `merapi endpoint list\|get\|add\|update\|delete` | manage OpenAPI operations |
| `merapi schema list\|get\|add\|delete` | manage `components/schemas` |
| `merapi security list\|add\|delete\|global` | manage `components/securitySchemes` + document-level defaults |
| `merapi validate [--warnings-as-errors]` | validate the document (exit 1 on errors) |
| `merapi test list\|show\|new\|scaffold\|run\|delete` | manage & run test scenarios |
| `merapi env list\|show\|add\|use\|delete` | manage environments |
| `merapi var list\|get\|set\|unset` | plain environment variables |
| `merapi secret list\|set\|get\|unset` | encrypted secrets |
| `merapi doc export\|preview\|markdown\|samples` | render documentation & code samples |
| `merapi gql validate\|show` | validate & inspect the GraphQL SDL schema (schema.graphql) |
| `merapi mock [--addr] [--delay] [--tunnel]` | serve mock responses for all documented endpoints (optionally exposed via ngrok/cloudflared); includes `POST /graphql` when an SDL schema is present |
| `merapi mcp` | run the MCP server for AI agents |
| `merapi version` | print version |

Global flags: `--json` (machine-readable output), `--env <name>` (override active environment), `-C <dir>` (project dir), `--quiet`.

Rich endpoints (many parameters, inline schemas, examples) can also be fed from a file:

```bash
merapi endpoint add -f endpoint.yaml       # or echo '{...}' | merapi endpoint add -f -
```

## Test scenarios

One YAML file per scenario, ordered steps, values captured from responses flow into later steps:

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
      headers:
        Authorization: "Bearer {{secret:API_TOKEN}}"
    expect:
      status: 200
      headers:
        Content-Type: application/json
      max_duration_ms: 1500
```

**Templates** (resolved in paths, headers, query, bodies *and in assertion values*):
- `{{base_url}}` — the environment's base URL
- `{{name}}` — environment variable, overridden by captures
- `{{secret:NAME}}` — decrypted secret (requires `MERAPI_SECRETS_KEY`)

**Assertions**: `status`, `status_in`, `headers` (exact, case-insensitive name), `body_contains`, `max_duration_ms`, and `json` with a JSONPath subset (`$.a.b[0]`, `$.items[*]` in captures). Expected JSON values may be literals, numbers, nested objects/arrays (subset match), or the markers `@exists`, `@not_exists`, `@null`. Assertion values support the same templates as requests, so a capture from one step can be asserted in the next (e.g. `"$.id": "{{pet_id}}"`).

`merapi test run [--filter x] [--fail-fast] [--timeout 30s] [--json]` — exit code is non-zero on failure, so it plugs into CI. `merapi test scaffold [--all|METHOD [path]]` generates starters with spec examples and `{{param}}` placeholders.

## Mock server

`merapi mock` serves deterministic responses for **every operation in the document**, so test scenarios (and any client) can run before the real backend exists:

```bash
merapi mock                          # http://127.0.0.1:9090
merapi mock --addr 0.0.0.0:8080 --delay 250   # custom address + latency (ms)
merapi mock --tunnel auto            # expose to the internet (ngrok/cloudflared)
merapi --env mock test run           # point an environment's base_url at the mock
```

- Each operation answers with its documented success response (lowest 2xx); the body is the response `example` when set, else generated from the response schema.
- **Stateful**: `POST` assigns an `id` and stores the resource — create→read-back lifecycle tests pass; `DELETE` evicts it.
- **Validation**: write bodies missing a schema-`required` field (or with an empty string) get the documented 4xx with an injected message — error paths are testable too.
- `X-Mock-Status: <code>` returns any other documented response (e.g. `X-Mock-Status: 404`) for exercising error handling.
- Path parameters fill matching response properties (`GET /pets/42` → `"id": 42`); `GET /` lists every mocked operation; wrong methods return `405` with an `Allow` header.
- **Auth enforcement**: operations with effective `security` reject requests without credentials using the documented 401/403 (a synthetic `401 {"error": …}` when neither is documented). The check is shape-only — the mock never validates token values.

**GraphQL** — when the project contains an SDL schema (`schema.graphql` or `graphql: <file>` in `merapi.yaml`), the mock also serves `POST /graphql`: it parses `{query, variables, operationName}`, builds a deterministic example response from the schema, and returns the standard GraphQL envelope. Parse/validation errors produce `{"errors": […]}` with HTTP 200 (GraphQL convention); `X-Mock-Status: <code>` overrides the status for error-path exploration.

**Tunneling** — `merapi mock --tunnel auto|ngrok|cloudflared` exposes the mock to the internet through the corresponding binary (must be installed and in `PATH`). The public HTTPS URL is printed once the tunnel is up — handy for testing webhooks (Stripe, GitHub, …) or sharing a temporary API with remote clients. `auto` picks the first available provider. Anyone with the URL can reach the mock, so stop it when you're done.

## Authentication

Auth is part of the API contract: declare the schemes once, then attach them to operations:

```bash
merapi security add BearerAuth --type http --scheme bearer --bearer-format JWT
merapi security add ApiKeyAuth --type apiKey --in header --name X-API-Key
merapi endpoint add GET /admin --security BearerAuth --response 200:OK --response 401:Unauthorized
merapi endpoint add GET /health --public          # explicitly public (security: [])
merapi security global --set BearerAuth           # document-level default for all operations
merapi security list                              # schemes + which operations use them
```

- `--security SCHEME[:SCOPE1,SCOPE2]` is repeatable — OR between items, AND within one item (OpenAPI semantics); `merapi validate` fails on references to undefined schemes.
- The **mock enforces** these requirements (missing credential → documented 401/403), and **code samples** inject placeholders like `Authorization: Bearer {{secret:BEARER_AUTH}}` / `X-API-Key: {{secret:X_API_KEY}}` — fill them via `merapi secret set BEARER_AUTH`.
- Supported scheme types: `apiKey` (header/query/cookie), `http` (bearer/basic/…), `oauth2`, `openIdConnect` (flows kept opaque), `mutualTLS`.

Design: [docs/design/auth.md](docs/design/auth.md).

## Code samples

`merapi doc samples <METHOD> <path>` prints ready-to-paste request samples in **cURL, Go, Python and JavaScript**, derived from the operation's parameters, request body and schema examples, using the active environment's base URL:

```bash
merapi doc samples GET /pets/{id} --lang curl,go
merapi doc samples POST /pets --base-url https://api.staging.example.com
```

Samples are also embedded in the generated Markdown reference (`merapi doc markdown`) and exposed to AI agents via the `doc_samples` MCP tool.

## Secrets

Secrets live in `.merapi/secrets.<env>.enc`: AES-256-GCM with a scrypt-derived key from `MERAPI_SECRETS_KEY`. Set it once (team-shared or in CI secrets):

```bash
export MERAPI_SECRETS_KEY="$(openssl rand -hex 32)"
merapi secret set API_TOKEN          # hidden prompt (recommended)
echo -n "$TOKEN" | merapi secret set API_TOKEN
```

Values are never echoed except by the explicit `merapi secret get`, never returned by MCP tools, and never written to test files — only `{{secret:NAME}}` references are.

## For AI agents (MCP)

```bash
merapi mcp          # stdio transport, inside a project directory
```

Agent setup:

```bash
# Claude Code
claude mcp add merapi -- merapi mcp

# generic MCP client config
{ "mcpServers": { "merapi": { "command": "merapi", "args": ["mcp"] } } }
```

Tools (all typed, errors returned as readable messages):

| Group | Tools |
|---|---|
| spec | `spec_summary`, `spec_validate` |
| endpoints | `endpoint_list`, `endpoint_get`, `endpoint_add`, `endpoint_update`, `endpoint_delete` |
| samples | `doc_samples` |
| schemas | `schema_list`, `schema_get`, `schema_add`, `schema_delete` |
| security | `security_list`, `security_add`, `security_delete` |
| graphql | `gql_validate`, `gql_show`, `gql_query` |
| tests | `test_list`, `test_get`, `test_create`, `test_scaffold`, `test_delete`, `test_run` |
| environments | `env_list`, `env_get`, `env_create`, `env_use`, `env_delete` |
| variables | `var_list`, `var_set`, `var_unset` |
| secrets | `secret_list`, `secret_set`, `secret_unset` (names only, values never exposed) |

Resources: `merapi://project` (overview), `merapi://openapi` (raw spec), `merapi://tests/{file}` (scenarios).

Every mutation is persisted immediately and keeps hand-written comments intact, so agents and humans can co-edit `openapi.yaml` safely. The server instructs agents to validate after edits — pair it with `merapi validate` in CI for a second gate.

## Development

```bash
make test     # unit + integration tests (incl. an in-memory MCP client session)
make vet
make demo     # builds and exercises the CLI in ./demo
```

Architecture: `internal/spec` (comment-preserving OpenAPI editing + validation), `internal/runner` (scenario execution), `internal/envs` (environments + encrypted secrets), `internal/templ` (strict template resolution), `internal/docs` (Swagger UI/Markdown/code samples), `internal/mock` (deterministic mock server), `internal/gql` (GraphQL SDL parsing, validation and example generation), `internal/mcpserver` (MCP tools/resources), `internal/cli` (cobra commands). Design notes: [docs/design/graphql.md](docs/design/graphql.md), [docs/design/auth.md](docs/design/auth.md).

## License

Licensed under the **Apache License, Version 2.0** — see [LICENSE](LICENSE) for the full text and [NOTICE](NOTICE) for attribution.

In short, you are free to:

- **Use** merapi for any purpose, including commercial
- **Copy, modify and redistribute** it (source or binary), including derived works
- **Sell** products or services built on it

Provided that you:

- Keep the `LICENSE` and `NOTICE` files (and attribution) intact
- State prominently that you modified the files (derivative works)
- Include a copy of the Apache 2.0 license with any redistribution

The license also grants an explicit **patent license** from contributors, with a patent-retaliation clause: if you sue a project contributor over patents related to the Work, your license to it terminates. The name "merapi" itself is **not** covered by the license — trademark use is separate (Section 6 of the license).

merapi is provided "AS IS", without warranty or liability (Sections 7–8).

### Contributing

Contributions are welcome under the same Apache 2.0 license (Section 5) — see
[CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, gates, conventions
and PR checklist.

### Third-party dependencies

merapi builds on open-source Go libraries (cobra, libopenapi, yaml.v3, the MCP Go SDK, gqlparser, x/crypto, …), each under its own permissive license (MIT/Apache-2.0/BSD). Run `go-licenses ./...` for a full report.
