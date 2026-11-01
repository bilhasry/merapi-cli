# Contributing to merapi

Thanks for your interest! All kinds of contributions are welcome: bug reports,
feature ideas, documentation, examples, and code.

## Ways to contribute

- **Bug reports & feature requests** — open a [GitHub issue](https://github.com/bilhasry/merapi-cli/issues).
- **Documentation** — README, `docs/`, examples, and design notes.
- **Code** — bug fixes, small improvements, or new features.
- **Ecosystem** — MCP tool coverage, GraphQL support, mock fidelity, CI integrations.

## Before you start

1. Search existing issues to avoid duplicates.
2. For anything **non-trivial**, open an issue first to agree on scope.
3. Non-trivial features follow the project's **design-note-first** pattern: write a
   short design doc in [`docs/design/`](docs/design/) (see
   [auth.md](docs/design/auth.md), [graphql.md](docs/design/graphql.md)) covering the
   model, what's in/out of scope, and boundaries — then implement it.

## Development setup

```bash
git clone https://github.com/bilhasry/merapi-cli.git
cd merapi-cli
make build     # -> bin/merapi (version injected via ldflags)
make test      # go test ./...
```

Requires Go ≥ 1.24 (see `go.mod`). Tests are hermetic — they use `t.TempDir()`
and `httptest`, no network or external services needed.

## Project layout

| Package | Responsibility |
|---|---|
| `cmd/merapi` | main entry point |
| `internal/spec` | comment-preserving OpenAPI editing + validation (libOpenAPI) |
| `internal/runner` | test-scenario execution |
| `internal/envs` | environments + encrypted secrets |
| `internal/templ` | strict `{{...}}` template resolution |
| `internal/docs` | Swagger UI / Markdown / code samples |
| `internal/mock` | deterministic mock server |
| `internal/gql` | GraphQL SDL parsing, validation, example generation |
| `internal/tunnel` | ngrok/cloudflared integration |
| `internal/mcpserver` | MCP tools + resources |
| `internal/cli` | cobra commands |

See [AGENTS.md](AGENTS.md) for the full working guide (CLI/MCP usage, workflows,
safety rules) — it is read by both humans and AI agents, so keep it in sync when
tools or workflows change.

## Workflow

1. **Fork** and create a branch — `feat/…`, `fix/…`, `docs/…`, `test/…`.
2. **Make your change**, with tests next to the code (`*_test.go`).
3. **Run the gates** — all must pass before a PR:

   ```bash
   go vet ./...
   go test ./...
   gofmt -l .      # must print nothing
   make build
   ```

4. **Update docs** when behavior changes:
   - user-facing → [README.md](README.md) (commands, features, tool tables)
   - agent-facing → [AGENTS.md](AGENTS.md) (MCP tool counts/tables, workflows)
   - design decisions → `docs/design/*.md`
5. **Open a PR** describing what changed, why, and how you tested it. Link the issue.

## Coding conventions

- Idiomatic, `gofmt`-clean Go; **standard library first** — justify new
  dependencies in the PR (current deps are kept deliberately small).
- Tests live next to the code as `*_test.go`; prefer table-driven tests.
- Comment exported functions and non-obvious logic — the codebase is heavily
  commented on purpose.
- **YAML edits must preserve comments** in untouched regions: use the node
  helpers in `internal/spec` (surgical edits), never re-marshal the whole document.
- Error messages should be **actionable** — follow the existing pattern of
  including a `fix:` hint the user can run.
- `merapi` CLI output conventions: `success`/`info` helpers for prose,
  `--json` on every command for machine-readable output.

## Adding an MCP tool (checklist)

MCP tools are the API agents see — keep them consistent:

1. Register the tool in `internal/mcpserver/server.go` with a typed
   input/output struct (`jsonschema:` tags on fields).
2. Add its name to the expected-tools list in `server_test.go`.
3. Add workflow coverage in `TestMCPFullWorkflow` (happy path + error path).
4. Update the tool tables **and counts** in README.md and AGENTS.md.
5. Mutations take `lock.Lock()`; never return secret values from any tool.

## Commit messages

Conventional style, matching existing history:

```
feat: add merapi security command group
fix: decode empty security list as explicitly public
docs: add problem statement section
chore: license the project under Apache License 2.0
```

Use a short subject (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`)
and add a body explaining **why** when the change isn't self-evident.

## Reporting bugs

Include:

- `merapi version` output, OS, and Go version (if building from source)
- a minimal reproduction (a scratch project, or the exact commands + output)
- expected vs. actual behavior
- `merapi validate --json` output when the document is involved

## Security

Please **don't** open a public issue for vulnerabilities — contact the
maintainer privately (GitHub profile) so it can be fixed before disclosure.

## License

By contributing, you agree that your contributions are licensed under the
**Apache License 2.0**, the same as the project (Section 5 of the license —
no separate CLA needed).
