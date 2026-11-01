package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/bilhasry/merapi/internal/project"
	"github.com/bilhasry/merapi/internal/spec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startTestSession boots the MCP server on an in-memory transport and returns
// a connected client session.
func startTestSession(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	server := newServer(root, "test")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func mustNotToolError(t *testing.T, res *mcp.CallToolResult, name string) {
	t.Helper()
	if res.IsError {
		text, _ := json.Marshal(res.Content)
		t.Fatalf("tool %s returned an error: %s", name, text)
	}
}

// mockPetAPI mirrors the documented behavior for the run-tests part.
func mockPetAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"Rex"}],"total":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPFullWorkflow(t *testing.T) {
	t.Setenv(envs.SecretsKeyEnvVar, "integration-test-passphrase")

	// Project scaffold.
	dir := t.TempDir()
	p, err := project.Init(dir, "mcp-test-api", false)
	if err != nil {
		t.Fatal(err)
	}
	doc := spec.NewSkeleton(p.SpecPath(), "MCP Test API", "1.0.0", "Managed by agents.", "http://localhost:1")
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := envs.Create(p, "dev", "http://localhost:1", false); err != nil {
		t.Fatal(err)
	}

	s := startTestSession(t, p.Root)
	ctx := context.Background()

	// 1. tools/list exposes the documented tool surface.
	names := map[string]bool{}
	for tool, err := range s.Tools(ctx, &mcp.ListToolsParams{}) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		names[tool.Name] = true
	}
	for _, want := range []string{
		"spec_summary", "spec_validate",
		"endpoint_list", "endpoint_get", "endpoint_add", "endpoint_update", "endpoint_delete",
		"doc_samples",
		"schema_list", "schema_get", "schema_add", "schema_delete",
		"security_list", "security_add", "security_delete",
		"test_list", "test_get", "test_create", "test_scaffold", "test_delete", "test_run",
		"env_list", "env_get", "env_create", "env_use", "env_delete",
		"var_list", "var_set", "var_unset",
		"secret_list", "secret_set", "secret_unset",
		"gql_validate", "gql_show", "gql_query",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}

	// 2. Agent documents a schema and an endpoint.
	res := callTool(t, s, "schema_add", map[string]any{
		"name": "Pet",
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":   map[string]any{"type": "integer"},
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"id"},
		},
	})
	mustNotToolError(t, res, "schema_add")

	res = callTool(t, s, "endpoint_add", map[string]any{
		"method":  "GET",
		"path":    "/pets",
		"summary": "List pets",
		"requestBody": map[string]any{
			"schemaRef": "Pet",
		},
		"responses": map[string]any{
			"200": map[string]any{"description": "A list of pets", "contentType": "application/json"},
		},
	})
	mustNotToolError(t, res, "endpoint_add")

	// The edit must be visible on disk immediately.
	specBytes, err := os.ReadFile(p.SpecPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(specBytes), "/pets:") || !strings.Contains(string(specBytes), "Pet:") {
		t.Fatalf("spec file not updated:\n%s", specBytes)
	}

	// 3. Validation passes (after filling doc gaps via update).
	res = callTool(t, s, "endpoint_update", map[string]any{
		"method":      "GET",
		"path":        "/pets",
		"description": "Returns every pet in the store.",
	})
	mustNotToolError(t, res, "endpoint_update")

	res = callTool(t, s, "spec_validate", nil)
	mustNotToolError(t, res, "spec_validate")
	valid, _ := decodeStructured[bool](res, "valid")
	if !valid {
		t.Fatalf("expected valid document, got: %+v", res.StructuredContent)
	}

	// 4. Environment, variable and secret.
	api := mockPetAPI(t)
	mustNotToolError(t, callTool(t, s, "env_create", map[string]any{
		"name": "staging", "baseUrl": api.URL,
	}), "env_create")
	mustNotToolError(t, callTool(t, s, "env_use", map[string]any{"name": "staging"}), "env_use")
	mustNotToolError(t, callTool(t, s, "var_set", map[string]any{
		"name": "tenant", "value": "acme",
	}), "var_set")
	mustNotToolError(t, callTool(t, s, "secret_set", map[string]any{
		"name": "API_TOKEN", "value": "test-token",
	}), "secret_set")

	// Secrets must never be readable back.
	res = callTool(t, s, "secret_list", nil)
	mustNotToolError(t, res, "secret_list")
	if strings.Contains(string(mustJSON(t, res)), "test-token") {
		t.Fatal("secret value leaked through secret_list")
	}

	// 5. Scaffold + run tests through the agent interface.
	// Duplicate add must surface as a tool error instead of corrupting the spec.
	dup := callTool(t, s, "endpoint_add", map[string]any{
		"method":  "GET",
		"path":    "/pets",
		"summary": "List pets again",
	})
	if !dup.IsError {
		t.Error("duplicate endpoint_add should return a tool error")
	}

	// Create an authenticated test scenario manually.
	res = callTool(t, s, "test_create", map[string]any{
		"name": "list pets",
		"steps": []any{
			map[string]any{
				"name": "lists pets with token",
				"request": map[string]any{
					"method":  "GET",
					"path":    "/pets",
					"headers": map[string]any{"Authorization": "Bearer {{secret:API_TOKEN}}"},
				},
				"expect": map[string]any{
					"status": 200,
					"json":   map[string]any{"$.total": 1, "$.data[0].name": "Rex"},
				},
			},
		},
	})
	mustNotToolError(t, res, "test_create")

	run := callTool(t, s, "test_run", map[string]any{"environment": "staging"})
	mustNotToolError(t, run, "test_run")
	decoded := decodeRunResult(t, run)
	if decoded.Failed != 0 || decoded.Passed != 1 {
		t.Fatalf("test_run result = %+v", decoded)
	}
	if decoded.Env != "staging" {
		t.Errorf("ran against env %q", decoded.Env)
	}

	// 6b. GraphQL tools — write an SDL file and exercise the gql_* tools.
	sdlPath := filepath.Join(p.Root, "schema.graphql")
	sdl := `
type Pet {
  id: ID!
  name: String!
}

type Query {
  pets: [Pet!]!
  pet(id: ID!): Pet
}
`
	if err := os.WriteFile(sdlPath, []byte(sdl), 0o644); err != nil {
		t.Fatal(err)
	}

	// gql_validate
	res = callTool(t, s, "gql_validate", nil)
	mustNotToolError(t, res, "gql_validate")
	if ok, _ := decodeStructured[bool](res, "ok"); !ok {
		t.Fatal("gql_validate should report ok=true for a valid schema")
	}

	// gql_show
	res = callTool(t, s, "gql_show", nil)
	mustNotToolError(t, res, "gql_show")
	showJSON := mustJSON(t, res)
	if !strings.Contains(string(showJSON), "pets") || !strings.Contains(string(showJSON), "Pet") {
		t.Errorf("gql_show output missing expected fields: %s", showJSON)
	}

	// gql_query
	res = callTool(t, s, "gql_query", map[string]any{
		"query": "{ pets { id name } }",
	})
	mustNotToolError(t, res, "gql_query")
	queryJSON := mustJSON(t, res)
	if !strings.Contains(string(queryJSON), "pets") || !strings.Contains(string(queryJSON), `"data"`) {
		t.Errorf("gql_query output missing data envelope: %s", queryJSON)
	}

	// gql_query with an invalid query returns errors but is not a tool error.
	res = callTool(t, s, "gql_query", map[string]any{"query": "{ nope }"})
	if res.IsError {
		t.Error("gql_query with an invalid query should return a GraphQL error envelope, not a tool error")
	}
	if !strings.Contains(string(mustJSON(t, res)), "errors") {
		t.Error("gql_query invalid query should include errors in the envelope")
	}

	// 6. Auth workflow: scheme → secured endpoint → dangling reference caught.
	res = callTool(t, s, "security_add", map[string]any{
		"name":         "BearerAuth",
		"type":         "http",
		"scheme":       "bearer",
		"bearerFormat": "JWT",
	})
	mustNotToolError(t, res, "security_add")

	res = callTool(t, s, "endpoint_add", map[string]any{
		"method":   "POST",
		"path":     "/pets/{id}/activate",
		"summary":  "Activate a pet",
		"security": []any{map[string]any{"BearerAuth": []any{}}},
	})
	mustNotToolError(t, res, "endpoint_add")

	// security_list shows the scheme and the operations using it.
	res = callTool(t, s, "security_list", nil)
	mustNotToolError(t, res, "security_list")
	listJSON := mustJSON(t, res)
	if !strings.Contains(string(listJSON), "BearerAuth") || !strings.Contains(string(listJSON), "activate") {
		t.Errorf("security_list output unexpected: %s", listJSON)
	}

	// Deleting a scheme still referenced by an operation must refuse.
	res = callTool(t, s, "security_delete", map[string]any{"name": "BearerAuth"})
	if !res.IsError {
		t.Error("security_delete of a referenced scheme should fail")
	}

	// A dangling scheme reference fails validation (the add itself succeeds).
	res = callTool(t, s, "endpoint_add", map[string]any{
		"method":   "GET",
		"path":     "/ghost",
		"summary":  "Ghost endpoint",
		"security": []any{map[string]any{"Ghost": []any{}}},
	})
	mustNotToolError(t, res, "endpoint_add")
	res = callTool(t, s, "spec_validate", nil)
	mustNotToolError(t, res, "spec_validate")
	if valid, _ := decodeStructured[bool](res, "valid"); valid {
		t.Error("spec_validate should flag the undefined Ghost scheme")
	}
	if !strings.Contains(string(mustJSON(t, res)), "Ghost") {
		t.Error("validation error should name the undefined scheme")
	}
	mustNotToolError(t, callTool(t, s, "endpoint_delete", map[string]any{"method": "GET", "path": "/ghost"}), "endpoint_delete")

	// 7. Resources.
	projectRes, err := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "merapi://project"})
	if err != nil {
		t.Fatalf("ReadResource(project): %v", err)
	}
	text := projectRes.Contents[0].Text
	if !strings.Contains(text, "MCP Test API") || !strings.Contains(text, "/pets") {
		t.Errorf("project resource unexpected:\n%s", text)
	}
	specRes, err := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "merapi://openapi"})
	if err != nil {
		t.Fatalf("ReadResource(openapi): %v", err)
	}
	if !strings.Contains(specRes.Contents[0].Text, "openapi: 3.1.0") {
		t.Error("openapi resource does not contain the spec")
	}
	files, _ := filepath.Glob(filepath.Join(p.TestsPath(), "*.yaml"))
	if len(files) != 1 {
		t.Fatalf("expected one test file on disk, got %v", files)
	}
	testRes, err := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "merapi://tests/" + filepath.Base(files[0])})
	if err != nil {
		t.Fatalf("ReadResource(tests): %v", err)
	}
	if !strings.Contains(testRes.Contents[0].Text, "list pets") {
		t.Error("test resource content unexpected")
	}

	// 7. Deletion path.
	mustNotToolError(t, callTool(t, s, "test_delete", map[string]any{"file": filepath.Base(files[0])}), "test_delete")
	mustNotToolError(t, callTool(t, s, "endpoint_delete", map[string]any{"method": "GET", "path": "/pets"}), "endpoint_delete")
	after, _ := os.ReadFile(p.SpecPath())
	if strings.Contains(string(after), "/pets:") {
		t.Error("endpoint not deleted from spec")
	}
}

// TestMCPRejectsMissingProject ensures the server reports a clear error when
// pointed at a non-project directory.
func TestMCPRejectsMissingProject(t *testing.T) {
	s := startTestSession(t, t.TempDir())
	res := callTool(t, s, "spec_summary", nil)
	if !res.IsError {
		t.Fatal("expected an error for a directory without merapi.yaml")
	}
}

func mustJSON(t *testing.T, res *mcp.CallToolResult) []byte {
	t.Helper()
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// decodeStructured reads one field from a tool's structured content.
func decodeStructured[T any](res *mcp.CallToolResult, field string) (T, bool) {
	var zero T
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return zero, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return zero, false
	}
	raw, ok := m[field]
	if !ok {
		return zero, false
	}
	out, err := roundTrip[T](raw)
	if err != nil {
		return zero, false
	}
	return out, true
}

func roundTrip[T any](v any) (T, error) {
	var out T
	data, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(data, &out)
}

func decodeRunResult(t *testing.T, res *mcp.CallToolResult) struct {
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
	Env    string `json:"env"`
} {
	t.Helper()
	var out struct {
		Passed int    `json:"passed"`
		Failed int    `json:"failed"`
		Env    string `json:"env"`
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
