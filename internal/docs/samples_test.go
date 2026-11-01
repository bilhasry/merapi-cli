package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilhasry/merapi/internal/spec"
)

func newSampleDoc(t *testing.T) *spec.Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Sample API
  version: 1.0.0
servers:
  - url: https://api.example.com/v1
paths:
  /pets:
    post:
      operationId: createPet
      parameters:
        - name: X-Trace-Id
          in: header
          required: true
        - name: dry_run
          in: query
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Pet'
      responses:
        "201":
          description: created
  /pets/{id}:
    get:
      operationId: getPet
      parameters:
        - name: id
          in: path
          required: true
          example: "42"
      responses:
        "200":
          description: ok
components:
  schemas:
    Pet:
      type: object
      properties:
        name:
          type: string
          example: Zed
        age:
          type: integer
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := spec.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestSamplesAllLanguages(t *testing.T) {
	doc := newSampleDoc(t)
	ep := doc.GetEndpoint("post", "/pets")
	if ep == nil {
		t.Fatal("endpoint not found")
	}
	samples, err := Samples(doc, ep, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != len(SampleLanguages()) {
		t.Fatalf("got %d samples, want %d", len(samples), len(SampleLanguages()))
	}

	byLang := map[string]string{}
	for _, s := range samples {
		byLang[s.Language] = s.Code
		if !strings.HasSuffix(s.Code, "\n") {
			t.Errorf("%s sample should end with a newline", s.Language)
		}
	}

	curl := byLang["curl"]
	for _, want := range []string{"curl -X POST", "https://api.example.com/v1/pets", "-H 'X-Trace-Id: <X-Trace-Id>'", "-d '{", `"name": "Zed"`} {
		if !strings.Contains(curl, want) {
			t.Errorf("curl sample missing %q:\n%s", want, curl)
		}
	}
	if strings.Contains(curl, "dry_run") {
		t.Errorf("optional query param without example should be omitted:\n%s", curl)
	}

	goCode := byLang["go"]
	for _, want := range []string{`http.NewRequest("POST"`, "api.example.com/v1/pets", `req.Header.Set("Content-Type", "application/json")`, `"name":"Zed"`} {
		if !strings.Contains(goCode, want) {
			t.Errorf("go sample missing %q:\n%s", want, goCode)
		}
	}

	py := byLang["python"]
	for _, want := range []string{"import requests", "requests.request(", `"name": "Zed"`, "print(resp.status_code"} {
		if !strings.Contains(py, want) {
			t.Errorf("python sample missing %q:\n%s", want, py)
		}
	}
	if strings.Contains(py, "true") {
		t.Errorf("python sample should use True not true:\n%s", py)
	}

	js := byLang["javascript"]
	for _, want := range []string{"await fetch(", "method: \"POST\"", "JSON.stringify(", `"name":"Zed"`} {
		if !strings.Contains(js, want) {
			t.Errorf("javascript sample missing %q:\n%s", want, js)
		}
	}
}

func TestSamplesPathParamsAndFilter(t *testing.T) {
	doc := newSampleDoc(t)
	ep := doc.GetEndpoint("GET", "/pets/{id}")
	if ep == nil {
		t.Fatal("endpoint not found")
	}
	samples, err := Samples(doc, ep, "https://staging.example.com/", []string{"curl", "python"})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}
	if !strings.Contains(samples[0].Code, "https://staging.example.com/pets/42") {
		t.Errorf("path param / base URL not substituted:\n%s", samples[0].Code)
	}
	if strings.Contains(samples[0].Code, "-d") {
		t.Errorf("GET sample should have no body:\n%s", samples[0].Code)
	}
	if strings.Contains(samples[0].Code, "requests") {
		t.Errorf("first filtered sample should be curl:\n%s", samples[0].Code)
	}
}

func TestSamplesUnknownLanguage(t *testing.T) {
	doc := newSampleDoc(t)
	ep := doc.GetEndpoint("get", "/pets/{id}")
	_, err := Samples(doc, ep, "", []string{"rust"})
	if err == nil {
		t.Fatal("expected error for unknown language")
	}
	if !strings.Contains(err.Error(), "rust") || !strings.Contains(err.Error(), "curl") {
		t.Errorf("error should name the bad slug and list supported ones: %v", err)
	}
}

func TestMarkdownIncludesSamples(t *testing.T) {
	doc := newSampleDoc(t)
	md := string(Markdown(doc))
	for _, want := range []string{"**Request examples**", "#### cURL", "#### Go", "#### Python", "#### JavaScript", "curl -X POST"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestPyLiteral(t *testing.T) {
	in := map[string]any{"ok": true, "n": 1.5, "none": nil, "list": []any{"a", false}}
	got := pyLiteral(in, 1)
	for _, want := range []string{"True", "1.5", "None", "[", "\"a\"", "False"} {
		if !strings.Contains(got, want) {
			t.Errorf("pyLiteral missing %q in:\n%s", want, got)
		}
	}
}

// --- auth injection -----------------------------------------------------------

func newSecureSampleDoc(t *testing.T) *spec.Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Secure Sample API
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      description: Requires a bearer token.
      security:
        - BearerAuth: []
      responses:
        "200":
          description: ok
  /admin/reset:
    post:
      operationId: reset
      summary: Reset data
      description: Requires an api key header.
      security:
        - ApiKeyAuth: []
      responses:
        "200":
          description: ok
  /public/health:
    get:
      operationId: health
      summary: Health
      description: Open endpoint.
      security: []
      responses:
        "200":
          description: ok
components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := spec.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestSamplesInjectBearerAuth(t *testing.T) {
	doc := newSecureSampleDoc(t)
	ep := doc.GetEndpoint("get", "/pets")
	samples, err := Samples(doc, ep, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if !strings.Contains(s.Code, "Authorization") || !strings.Contains(s.Code, "BEARER_AUTH") {
			t.Errorf("%s sample missing bearer auth placeholder:\n%s", s.Language, s.Code)
		}
		if !strings.Contains(s.Code, "Bearer {{secret:BEARER_AUTH}}") {
			t.Errorf("%s sample should use the Bearer scheme prefix:\n%s", s.Language, s.Code)
		}
	}
}

func TestSamplesInjectAPIKeyHeader(t *testing.T) {
	doc := newSecureSampleDoc(t)
	ep := doc.GetEndpoint("post", "/admin/reset")
	samples, err := Samples(doc, ep, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if !strings.Contains(s.Code, "X-API-Key") || !strings.Contains(s.Code, "X_API_KEY") {
			t.Errorf("%s sample missing api key placeholder:\n%s", s.Language, s.Code)
		}
	}
}

func TestSamplesPublicOperationHasNoAuth(t *testing.T) {
	doc := newSecureSampleDoc(t)
	ep := doc.GetEndpoint("get", "/public/health")
	samples, err := Samples(doc, ep, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if strings.Contains(s.Code, "secret:") || strings.Contains(s.Code, "Authorization") {
			t.Errorf("%s sample of a public operation must not contain auth:\n%s", s.Language, s.Code)
		}
	}
}
