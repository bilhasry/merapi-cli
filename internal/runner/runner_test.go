package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bilhasry/merapi/internal/spec"
)

// mockAPI is a tiny in-memory API used by the runner tests.
func mockAPI(t *testing.T) *httptest.Server {
	t.Helper()
	pets := map[string]any{
		"data":  []any{map[string]any{"id": 1, "name": "Rex"}, map[string]any{"id": 2, "name": "Milo"}},
		"total": 2,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pets)
	})
	mux.HandleFunc("POST /pets", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 3, "name": body["name"]})
	})
	mux.HandleFunc("GET /pets/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"Rex"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeScenario(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPassingScenarioWithCaptures(t *testing.T) {
	srv := mockAPI(t)
	dir := t.TempDir()
	writeScenario(t, dir, "lifecycle.yaml", `
name: pet lifecycle
steps:
  - name: create pet
    request:
      method: POST
      path: /pets
      json:
        name: Zed
    expect:
      status: 201
      json:
        "$.id": "@exists"
        "$.name": Zed
    capture:
      new_id: "$.id"
  - name: fetch first pet
    request:
      method: GET
      path: /pets/1
      headers:
        Authorization: "Bearer test-token"
    expect:
      status: 200
      headers:
        Content-Type: application/json
      json:
        "$.name": Rex
        "$.missing": "@not_exists"
`)
	result, err := Run(dir, Options{BaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Success() {
		t.Fatalf("expected pass, got %+v", result)
	}
	if result.Passed != 1 {
		t.Errorf("passed = %d", result.Passed)
	}
	if got := result.Scenarios[0].Steps[0].Status; got != 201 {
		t.Errorf("step1 status = %d", got)
	}
}

func TestRunFailingAssertions(t *testing.T) {
	srv := mockAPI(t)
	dir := t.TempDir()
	writeScenario(t, dir, "fail.yaml", `
name: failing scenario
steps:
  - name: unauthorized list
    request:
      method: GET
      path: /pets
    expect:
      status: 200
      json:
        "$.total": 5
`)
	result, err := Run(dir, Options{BaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Success() {
		t.Fatal("expected failure")
	}
	failures := result.Scenarios[0].Steps[0].Failures
	joined := strings.Join(failures, " | ")
	if !strings.Contains(joined, "expected status 200, got 401") {
		t.Errorf("missing status failure in %q", joined)
	}
	if !strings.Contains(joined, "expected number 5") && !strings.Contains(joined, "expected 5") {
		t.Errorf("missing json assertion failure in %q", joined)
	}
}

func TestRunStopsScenarioAfterFailedStep(t *testing.T) {
	srv := mockAPI(t)
	dir := t.TempDir()
	writeScenario(t, dir, "chain.yaml", `
name: chain
steps:
  - name: first fails
    request: {method: GET, path: /pets}
    expect: {status: 200}
  - name: never runs
    request: {method: GET, path: /pets/1}
    expect: {status: 200}
`)
	result, err := Run(dir, Options{BaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	steps := result.Scenarios[0].Steps
	if len(steps) != 1 {
		t.Errorf("expected remaining steps to be skipped, got %d steps", len(steps))
	}
}

func TestTemplateFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "bad-template.yaml", `
name: bad template
steps:
  - request:
      path: /pets/{{undefined_thing}}
`)
	result, err := Run(dir, Options{BaseURL: "http://localhost", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success() {
		t.Fatal("expected failure for undefined template variable")
	}
	if !strings.Contains(result.Scenarios[0].Steps[0].Failures[0], "undefined_thing") {
		t.Errorf("failure = %v", result.Scenarios[0].Steps[0].Failures)
	}
}

func TestSecretResolution(t *testing.T) {
	srv := mockAPI(t)
	dir := t.TempDir()
	writeScenario(t, dir, "secret.yaml", `
name: uses secret
steps:
  - request:
      method: GET
      path: /pets
      headers:
        Authorization: "Bearer {{secret:API_TOKEN}}"
    expect: {status: 200}
`)
	result, err := Run(dir, Options{
		BaseURL: srv.URL,
		Secrets: map[string]string{"API_TOKEN": "test-token"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success() {
		t.Fatalf("expected pass with secrets, got %+v", result)
	}
}

func TestTemplatesInAssertions(t *testing.T) {
	srv := mockAPI(t)
	dir := t.TempDir()
	writeScenario(t, dir, "assert-templates.yaml", `
name: templated assertions
steps:
  - name: create pet
    request:
      method: POST
      path: /pets
      json:
        name: Templ
    expect:
      status: 201
    capture:
      pet_id: "$.id"
      pet_name: "$.name"
  - name: captured values flow into later requests and assertions
    request:
      method: POST
      path: /pets
      json:
        name: "{{pet_name}}"
    expect:
      status: 201
      body_contains: ["{{pet_name}}"]
      json:
        "$.name": "{{pet_name}}"
        "$.id": "@exists"
`)
	result, err := Run(dir, Options{BaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success() {
		t.Fatalf("expected pass, got %+v", result.Scenarios[0].Steps)
	}
}

func TestFilter(t *testing.T) {
	srv := mockAPI(t)
	dir := t.TempDir()
	writeScenario(t, dir, "aaa.yaml", "name: aaa\nsteps:\n  - request: {method: GET, path: /pets/1}\n    expect: {status: 200}\n")
	writeScenario(t, dir, "bbb.yaml", "name: bbb\nsteps:\n  - request: {method: GET, path: /nope}\n    expect: {status: 200}\n")
	result, err := Run(dir, Options{BaseURL: srv.URL, Filter: "aaa", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scenarios) != 1 || !result.Success() {
		t.Errorf("filtered run = %+v", result)
	}
}

func TestJSONPath(t *testing.T) {
	var data any
	_ = json.Unmarshal([]byte(`{"a":{"b":[10,20,{"c":"x"}]},"list":[{"id":1},{"id":2}]}`), &data)

	cases := []struct {
		path string
		want string
	}{
		{"$.a.b[0]", "10"},
		{"$.a.b[2].c", "x"},
		{"$.a.b[99]", "null"},
		{"$.nothing.here", "null"},
	}
	for _, c := range cases {
		got, err := jsonPath(data, c.path)
		if err != nil {
			t.Errorf("jsonPath(%q): %v", c.path, err)
			continue
		}
		if stringify(got) != c.want {
			t.Errorf("jsonPath(%q) = %v, want %v", c.path, stringify(got), c.want)
		}
	}

	// Wildcard capture.
	got, err := jsonPath(data, "$.list[*].id")
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.([]any)
	if !ok || len(arr) != 2 {
		t.Errorf("wildcard result = %v", got)
	}

	// Invalid paths error out.
	if _, err := jsonPath(data, "a.b"); err == nil {
		t.Error("path without $ should error")
	}
	if _, err := jsonPath(data, "$.a[?x]"); err == nil {
		t.Error("unsupported filter syntax should error")
	}
}

func TestScaffoldFromSpec(t *testing.T) {
	// Scaffold from a spec built with the spec package.
	specDir := t.TempDir()
	specPath := filepath.Join(specDir, "openapi.yaml")
	specContent := `openapi: 3.1.0
info:
  title: Scaffold API
  version: 1.0.0
paths:
  /pets/{id}:
    get:
      operationId: getPetById
      summary: Get a pet
      parameters:
        - name: id
          in: path
          required: true
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Pet'
        "404":
          description: Missing
components:
  schemas:
    Pet:
      type: object
`
	if err := os.WriteFile(specPath, []byte(specContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use the spec package through the runner's public scaffold API.
	// (spec.Document is imported lazily to keep this test focused on output files.)
	results, err := scaffoldViaSpecPackage(specPath, t.TempDir())
	if err != nil {
		t.Fatalf("ScaffoldAll: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("generated %d files, want 1", len(results))
	}
	data, err := os.ReadFile(results[0].File)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"name: GET /pets/{id}", "status: 200", "{{id}}", "# This test needs variables: id"} {
		if !strings.Contains(content, want) {
			t.Errorf("scaffolded file missing %q:\n%s", want, content)
		}
	}
	// Second scaffold run without force skips.
	results2, err := scaffoldViaSpecPackage(specPath, filepath.Dir(results[0].File))
	if err != nil {
		t.Fatal(err)
	}
	if !results2[0].Skipped {
		t.Error("expected skip on existing file")
	}
}

// scaffoldViaSpecPackage loads the spec and scaffolds scenarios into outDir.
func scaffoldViaSpecPackage(specPath, outDir string) ([]ScaffoldResult, error) {
	doc, err := spec.Load(specPath)
	if err != nil {
		return nil, err
	}
	return ScaffoldAll(doc, outDir, ScaffoldOptions{})
}
