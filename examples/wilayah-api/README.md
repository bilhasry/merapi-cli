# Example: Indonesia Wilayah API

A complete `merapi` project documenting a small public-data API: the
**provinces of Indonesia and their cities (kabupaten/kota)**, using BPS codes
(`31` = DKI Jakarta, `3171` = Jakarta Pusat).

## Try it

From the repository root:

```bash
make build
cd examples/wilayah-api

merapi validate                       # spec is valid + fully documented
merapi mock &                          # serve every documented endpoint on :9292
merapi --env local test run            # 5 scenarios against the mock
merapi --env local doc samples GET /cities/{id}   # ready-to-paste samples
```

Or as one shot: `make example` (builds, starts the mock, runs the tests, stops
the mock).

## What it demonstrates

- **Schemas with real examples** — `Province`, `City` (with a
  `type: kabupaten|kota` enum), list wrappers `ProvinceList`/`CityList`, and
  `Error`. The examples (`DKI Jakarta`, `Jakarta Pusat`) drive the mock
  responses, the Markdown docs, and the code samples.
- **Path & query parameters** — `/provinces/{id}`, `/cities/{id}` (path
  examples show up in samples as `/cities/3171`), and the optional
  `?provinceId=` filter on `/cities`.
- **GraphQL** — `schema.graphql` mirrors the REST models in SDL: the same
  `merapi mock` also answers `POST /graphql` with deterministic examples.
  Try it: `merapi gql show`, or
  `curl -X POST localhost:9090/graphql -d '{"query":"{ provinces { count data { name } } }"}'`.
- **Test scenarios** (`tests/`):
  - JSONPath assertions (`$.data[0].name`, `@exists` markers)
  - environment variables (`{{default_province}}` from `environments/local.yaml`)
  - a cross-step capture (`province_code: "$.code"`)
  - exercising a documented error path with `X-Mock-Status: 404`
- **Environments** — `dev` (the server from `merapi init`) and `local`
  (pointed at the mock at `http://127.0.0.1:9292`).

## Layout

```
wilayah-api/
├── merapi.yaml           # project config (incl. graphql: schema.graphql)
├── openapi.yaml          # OpenAPI 3.1 document (5 operations)
├── schema.graphql        # GraphQL SDL served by `merapi mock`
├── environments/         # dev + local (base_url + variables)
├── tests/                # 5 scenarios, all green against the mock
└── API.md                # generated reference (merapi doc markdown)
```

Regenerate the docs with `merapi doc markdown -o API.md`.
