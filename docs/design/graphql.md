# Design: GraphQL support in merapi

Status: phase 1 implemented · Scope: SDL documentation + mock. Short by
design — decisions, boundaries, and what's next.

## Why one CLI (not `merapi-graphql`)

GraphQL is an API style, not a separate product. Real projects mix REST and
GraphQL (e.g. a REST API plus a gateway), and our value is *one project, one
environment store, one `test run` exit code, one MCP server for agents*.
A second binary would split all of that. Everything GraphQL therefore lives
inside merapi, namespaced under `gql`, and existing REST behavior never
changes.

## Model

A project may contain **both** an OpenAPI document and a GraphQL SDL file:

```
my-api/
├── merapi.yaml        # may set `graphql: schema.graphql`
├── openapi.yaml       # REST operations (optional if the API is pure GraphQL)
└── schema.graphql     # GraphQL SDL (types, Query, Mutation) — convention default
```

- Discovery: `merapi.yaml` key `graphql` (default `schema.graphql` at the
  project root). No file → all `gql` commands report a clear "not configured"
  error; the mock serves REST only.
- Parsing/validation: [`vektah/gqlparser`](https://github.com/vektah/gqlparser)
  (battle-tested, pure Go: SDL AST + spec validation). We do not hand-roll a
  GraphQL parser.

## Phase 1 (this change)

| Piece | Behavior |
|---|---|
| `internal/gql` | Load/validate SDL; summarize types + operations; **build example responses** by walking a query's selection set against the SDL (scalars → deterministic values, enums → first value, objects → recurse, lists → one item, `__typename` supported) |
| `merapi gql validate` | Spec-validate `schema.graphql`; exit 1 on errors (CI-friendly, like `merapi validate`) |
| `merapi gql show` | Types, queries, mutations with arguments (`--json` for agents) |
| `merapi mock` | When an SDL file is present, also serves `POST /graphql`: parses `{query, variables, operationName}`, answers `{"data": …}` from the schema; parse/validation errors → `{"errors":[{…}]}` with HTTP 200 (GraphQL convention); `X-Mock-Status` override still honored for REST routes |

Deliberately excluded from phase 1 (see phase 2): subscriptions, fragments in
example generation (inline fragments are skipped with a warning), persisted
queries, federation, Relay connection conventions.

## Phase 2 (next)

1. **Test scenarios**: a `graphql:` request type — same steps, captures,
   templates and JSONPath assertions:
   ```yaml
   - name: fetch a province
     request:
       graphql:
         query: "query ($id: ID!) { province(id: $id) { id name } }"
         variables: { id: "{{default_province}}" }
         operationName: province   # optional; default: first operation
     expect:
       json: { "$.data.province.name": "@exists" }
   ```
2. `doc_samples`/Markdown: a `graphql` sample (curl POST + JS fetch).
3. MCP: `gql_validate`, `gql_show`, `gql_query` tools.
4. `doc export`/Markdown: embed the SDL reference.

## Boundaries

- The REST pipeline (spec, runner, docs, MCP) keeps working untouched when no
  SDL file exists; GraphQL is additive everywhere.
- Mock example generation is deterministic (stable output for tests), derived
  only from the SDL — no fake randomness.
- Secrets rules are unchanged: nothing GraphQL-specific may print or persist
  secret values.
