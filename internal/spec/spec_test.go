package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestDoc(t *testing.T) *Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	doc := NewSkeleton(path, "Test API", "1.0.0", "Test API description", "http://localhost:9999")
	if err := doc.Save(); err != nil {
		t.Fatalf("saving skeleton: %v", err)
	}
	return doc
}

func TestAddGetUpdateDeleteEndpoint(t *testing.T) {
	doc := newTestDoc(t)

	ep := &Endpoint{
		Method:      "post",
		Path:        "/pets/{id}",
		Summary:     "Update a pet",
		Description: "Updates an existing pet by id.",
		RequestBody: &RequestBody{SchemaRef: "Pet", Example: map[string]any{"name": "Rex"}},
		Responses: map[string]Response{
			"200": {Description: "Updated", SchemaRef: "Pet"},
			"404": {Description: "Not found"},
		},
		Parameters: []Parameter{{Name: "id", In: "path", Required: true}},
	}
	if err := doc.AddEndpoint(ep); err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	if ep.OperationID != "postPetsById" {
		t.Errorf("generated operationId = %q, want postPetsById", ep.OperationID)
	}
	if len(ep.Tags) != 1 || ep.Tags[0] != "pets" {
		t.Errorf("derived tags = %v, want [pets]", ep.Tags)
	}

	got := doc.GetEndpoint("POST", "/pets/{id}")
	if got == nil {
		t.Fatal("GetEndpoint returned nil after add")
	}
	if got.Summary != "Update a pet" {
		t.Errorf("summary = %q", got.Summary)
	}
	if got.RequestBody == nil || got.RequestBody.SchemaRef != "Pet" {
		t.Errorf("requestBody schemaRef = %+v", got.RequestBody)
	}
	if len(got.Responses) != 2 {
		t.Errorf("responses = %v", got.Responses)
	}

	// Duplicate add must fail.
	if err := doc.AddEndpoint(&Endpoint{Method: "POST", Path: "/pets/{id}"}); err == nil {
		t.Error("AddEndpoint on existing operation should fail")
	}

	// Update.
	if err := doc.UpdateEndpoint(&Endpoint{
		Method:  "POST",
		Path:    "/pets/{id}",
		Summary: "Replace a pet",
		Tags:    []string{"pets", "write"},
	}, false); err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	got = doc.GetEndpoint("post", "/pets/{id}")
	if got.Summary != "Replace a pet" || len(got.Tags) != 2 {
		t.Errorf("after update: summary=%q tags=%v", got.Summary, got.Tags)
	}
	// Untouched fields survive updates.
	if got.Description != "Updates an existing pet by id." {
		t.Errorf("description lost on update: %q", got.Description)
	}

	// Delete.
	if err := doc.DeleteEndpoint("POST", "/pets/{id}"); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	if doc.GetEndpoint("POST", "/pets/{id}") != nil {
		t.Error("endpoint still present after delete")
	}
	if err := doc.DeleteEndpoint("POST", "/pets/{id}"); err == nil {
		t.Error("second delete should fail with not-found")
	}
}

func TestCommentPreservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Commented API   # the title
  version: 1.0.0
# paths are documented below
paths:
  /health:
    get:
      operationId: getHealth
      summary: Health check   # important
      responses:
        "200":
          description: OK
components:
  schemas: {}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := doc.AddEndpoint(&Endpoint{Method: "GET", Path: "/pets", Summary: "List pets"}); err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	if err := doc.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, _ := os.ReadFile(path)
	for _, want := range []string{"# the title", "# paths are documented below", "# important"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("comment %q lost after edit:\n%s", want, out)
		}
	}
	if !strings.Contains(string(out), "/pets:") {
		t.Error("new endpoint missing from output")
	}
}

func TestSchemaCRUDAndDeleteGuard(t *testing.T) {
	doc := newTestDoc(t)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "integer"},
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"id"},
	}
	if err := doc.AddSchema("Pet", schema, false); err != nil {
		t.Fatalf("AddSchema: %v", err)
	}
	if err := doc.AddSchema("Pet", schema, false); err == nil {
		t.Error("duplicate AddSchema should fail without force")
	}
	if err := doc.AddSchema("Pet", schema, true); err != nil {
		t.Fatalf("AddSchema(force): %v", err)
	}

	summaries := doc.ListSchemas()
	if len(summaries) != 1 || summaries[0].Name != "Pet" || summaries[0].Properties != 2 {
		t.Errorf("ListSchemas = %+v", summaries)
	}

	// Delete guard: referenced schema cannot be deleted.
	if err := doc.AddEndpoint(&Endpoint{
		Method:    "GET",
		Path:      "/pets",
		Responses: map[string]Response{"200": {Description: "OK", SchemaRef: "Pet"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := doc.DeleteSchema("Pet"); err == nil {
		t.Error("DeleteSchema should refuse while referenced")
	}
	if err := doc.DeleteEndpoint("GET", "/pets"); err != nil {
		t.Fatal(err)
	}
	if err := doc.DeleteSchema("Pet"); err != nil {
		t.Errorf("DeleteSchema after unreferencing: %v", err)
	}
}

func TestValidate(t *testing.T) {
	doc := newTestDoc(t)
	if err := doc.AddEndpoint(&Endpoint{Method: "GET", Path: "/pets", Summary: "List pets", Description: "Lists all pets."}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	issues, err := doc.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if HasErrors(issues) {
		t.Fatalf("expected no errors, got %+v", issues)
	}

	// Break the document: a path item with an invalid method key.
	broken := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(broken, []byte("openapi: 3.1.0\ninfo:\n  title: X\n  version: '1'\npaths:\n  /x:\n    banana:\n      responses: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bdoc, err := Load(broken)
	if err != nil {
		t.Fatal(err)
	}
	issues, err = bdoc.Validate()
	if err != nil {
		t.Fatalf("Validate(broken): %v", err)
	}
	if !HasErrors(issues) {
		t.Errorf("expected errors for invalid document, got %+v", issues)
	}
}

func TestQualityWarnings(t *testing.T) {
	doc := newTestDoc(t)
	if err := doc.AddEndpoint(&Endpoint{Method: "GET", Path: "/pets"}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	issues, err := doc.Validate()
	if err != nil {
		t.Fatal(err)
	}
	var missingSummary, missingDescription bool
	for _, issue := range issues {
		if strings.Contains(issue.Message, "no summary") {
			missingSummary = true
		}
		if strings.Contains(issue.Message, "no description") {
			missingDescription = true
		}
	}
	if !missingSummary || !missingDescription {
		t.Errorf("expected quality warnings, got %+v", issues)
	}
}

func TestGenerateOperationID(t *testing.T) {
	cases := map[string]string{
		"GET /pets":           "getPets",
		"GET /pets/{id}":      "getPetsById",
		"POST /user-profiles": "postUserProfiles",
		"DELETE /a/b/c":       "deleteABC",
	}
	for in, want := range cases {
		parts := strings.SplitN(in, " ", 2)
		if got := GenerateOperationID(parts[0], parts[1]); got != want {
			t.Errorf("GenerateOperationID(%s) = %q, want %q", in, got, want)
		}
	}
}

// --- security -----------------------------------------------------------------

func TestSecuritySchemeCRUD(t *testing.T) {
	doc := newTestDoc(t)

	// apiKey scheme.
	if err := doc.AddSecurityScheme(SecurityScheme{
		Name: "ApiKeyAuth", Type: "apiKey", In: "header", ParameterName: "X-API-Key",
	}); err != nil {
		t.Fatalf("AddSecurityScheme(apiKey): %v", err)
	}
	// http bearer scheme with format.
	if err := doc.AddSecurityScheme(SecurityScheme{
		Name: "BearerAuth", Type: "http", Scheme: "bearer", BearerFormat: "JWT",
	}); err != nil {
		t.Fatalf("AddSecurityScheme(http): %v", err)
	}

	schemes := doc.ListSecuritySchemes()
	if len(schemes) != 2 {
		t.Fatalf("schemes = %d, want 2", len(schemes))
	}
	bearer := doc.GetSecurityScheme("BearerAuth")
	if bearer == nil || bearer.Scheme != "bearer" || bearer.BearerFormat != "JWT" {
		t.Errorf("BearerAuth = %+v", bearer)
	}
	if d := bearer.Describe(); d != "http bearer (JWT)" {
		t.Errorf("Describe() = %q", d)
	}
	key := doc.GetSecurityScheme("ApiKeyAuth")
	if key == nil || key.In != "header" || key.ParameterName != "X-API-Key" {
		t.Errorf("ApiKeyAuth = %+v", key)
	}

	// Validation errors.
	if err := doc.AddSecurityScheme(SecurityScheme{Name: "Bad", Type: "apiKey", In: "body", ParameterName: "x"}); err == nil {
		t.Error("apiKey with invalid --in should fail")
	}
	if err := doc.AddSecurityScheme(SecurityScheme{Name: "Bad", Type: "http"}); err == nil {
		t.Error("http without scheme should fail")
	}
	if err := doc.AddSecurityScheme(SecurityScheme{Name: "Bad", Type: "nope"}); err == nil {
		t.Error("unknown type should fail")
	}

	// Round-trip through disk.
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetSecurityScheme("ApiKeyAuth"); got == nil || got.ParameterName != "X-API-Key" {
		t.Errorf("after reload ApiKeyAuth = %+v", got)
	}

	// Delete.
	if err := doc.DeleteSecurityScheme("BearerAuth"); err != nil {
		t.Fatalf("DeleteSecurityScheme: %v", err)
	}
	if doc.GetSecurityScheme("BearerAuth") != nil {
		t.Error("scheme still present after delete")
	}
	if err := doc.DeleteSecurityScheme("BearerAuth"); err == nil {
		t.Error("second delete should fail")
	}
}

func TestEndpointSecurityRoundTrip(t *testing.T) {
	doc := newTestDoc(t)
	if err := doc.AddSecurityScheme(SecurityScheme{Name: "BearerAuth", Type: "http", Scheme: "bearer"}); err != nil {
		t.Fatal(err)
	}

	// Add an endpoint with security requirements.
	if err := doc.AddEndpoint(&Endpoint{
		Method: "GET", Path: "/pets", Summary: "List pets",
		Security: []map[string][]string{{"BearerAuth": []string{}}},
	}); err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	got := doc.GetEndpoint("GET", "/pets")
	if got == nil || len(got.Security) != 1 {
		t.Fatalf("security not decoded: %+v", got)
	}
	if _, ok := got.Security[0]["BearerAuth"]; !ok {
		t.Errorf("security = %+v", got.Security)
	}

	// Update clears security to "explicitly public" (empty list).
	if err := doc.UpdateEndpoint(&Endpoint{
		Method: "GET", Path: "/pets", Security: []map[string][]string{},
	}, false); err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	got = doc.GetEndpoint("GET", "/pets")
	if got == nil || got.Security == nil || len(got.Security) != 0 {
		t.Errorf("security should be an empty (public) list, got %+v", got.Security)
	}

	// Update with nil security leaves the field untouched.
	if err := doc.UpdateEndpoint(&Endpoint{Method: "GET", Path: "/pets", Summary: "x"}, false); err != nil {
		t.Fatal(err)
	}
	got = doc.GetEndpoint("GET", "/pets")
	if got.Security == nil || len(got.Security) != 0 {
		t.Errorf("nil update should keep empty security, got %+v", got.Security)
	}
}

func TestDocumentLevelSecurity(t *testing.T) {
	doc := newTestDoc(t)
	if err := doc.AddSecurityScheme(SecurityScheme{Name: "BearerAuth", Type: "http", Scheme: "bearer"}); err != nil {
		t.Fatal(err)
	}
	doc.SetSecurity([]map[string][]string{{"BearerAuth": {"read", "write"}}})

	if reqs := doc.Security(); len(reqs) != 1 || reqs[0]["BearerAuth"][0] != "read" {
		t.Errorf("document security = %+v", reqs)
	}

	// Persist + reload.
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if reqs := reloaded.Security(); len(reqs) != 1 {
		t.Errorf("after reload document security = %+v", reqs)
	}

	// Deleting a scheme still referenced at document level must refuse.
	if err := doc.DeleteSecurityScheme("BearerAuth"); err == nil {
		t.Error("deleting a document-level scheme should fail")
	}
}

func TestUndefinedSecuritySchemeValidation(t *testing.T) {
	doc := newTestDoc(t)
	if err := doc.AddEndpoint(&Endpoint{
		Method: "GET", Path: "/pets", Summary: "s", Description: "d",
		Security: []map[string][]string{{"Ghost": {}}},
	}); err != nil {
		t.Fatal(err)
	}
	undef := doc.UndefinedSecuritySchemes()
	if len(undef) != 1 || !strings.Contains(undef[0], "Ghost") {
		t.Errorf("undefined = %v", undef)
	}
	issues, err := doc.Validate()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range issues {
		if issue.Severity == "error" && strings.Contains(issue.Message, "Ghost") {
			found = true
		}
	}
	if !found {
		t.Errorf("validator should flag undefined scheme, got %+v", issues)
	}

	// Defining the scheme clears the error.
	if err := doc.AddSecurityScheme(SecurityScheme{Name: "Ghost", Type: "http", Scheme: "bearer"}); err != nil {
		t.Fatal(err)
	}
	if got := doc.UndefinedSecuritySchemes(); len(got) != 0 {
		t.Errorf("after defining, undefined = %v", got)
	}
}

func TestSecuritySchemesUsing(t *testing.T) {
	doc := newTestDoc(t)
	if err := doc.AddSecurityScheme(SecurityScheme{Name: "BearerAuth", Type: "http", Scheme: "bearer"}); err != nil {
		t.Fatal(err)
	}
	if err := doc.AddEndpoint(&Endpoint{
		Method: "GET", Path: "/pets", Summary: "s",
		Security: []map[string][]string{{"BearerAuth": {}}},
	}); err != nil {
		t.Fatal(err)
	}
	using := doc.SecuritySchemesUsing()
	if len(using["BearerAuth"]) != 1 || using["BearerAuth"][0] != "GET /pets" {
		t.Errorf("using = %+v", using)
	}
}
