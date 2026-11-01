package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bilhasry/merapi/internal/gql"
	"github.com/bilhasry/merapi/internal/spec"
)

func newMockDoc(t *testing.T) *spec.Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Mock API
  version: 1.0.0
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  total:
                    type: integer
                  data:
                    type: array
                    items:
                      $ref: '#/components/schemas/Pet'
    post:
      operationId: createPet
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Pet'
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Pet'
        "400":
          description: bad request
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
  /pets/{id}:
    get:
      operationId: getPet
      parameters:
        - name: id
          in: path
          required: true
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Pet'
        "404":
          description: missing
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
    delete:
      operationId: deletePet
      parameters:
        - name: id
          in: path
          required: true
      responses:
        "204":
          description: deleted
  /health:
    get:
      operationId: health
      responses:
        "200":
          description: ok
          content:
            application/json:
              example:
                status: up
components:
  schemas:
    Pet:
      type: object
      properties:
        id:
          type: integer
        name:
          type: string
          example: Rex
      required:
        - name
    Error:
      type: object
      properties:
        code:
          type: integer
        message:
          type: string
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

func do(t *testing.T, h http.Handler, method, path string, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

func TestMockListAndIndex(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()

	rec := do(t, h, "GET", "/", "", nil)
	if rec.Code != 200 {
		t.Fatalf("index status = %d", rec.Code)
	}
	idx := decode(t, rec)
	if idx["service"] != "Mock API" {
		t.Errorf("index service = %v", idx["service"])
	}
	ops := idx["operations"].([]any)
	if len(ops) != 5 {
		t.Errorf("index operations = %d, want 5", len(ops))
	}

	rec = do(t, h, "GET", "/pets", "", nil)
	if rec.Code != 200 {
		t.Fatalf("GET /pets status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	got := decode(t, rec)
	if _, ok := got["total"]; !ok {
		t.Errorf("schema-derived body missing total: %v", got)
	}
	if data, ok := got["data"].([]any); !ok || len(data) == 0 {
		t.Errorf("data should be a non-empty array: %v", got["data"])
	}
}

func TestMockPathParamEcho(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()
	rec := do(t, h, "GET", "/pets/42", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	got := decode(t, rec)
	if got["id"] != float64(42) {
		t.Errorf("id = %v, want 42 (path param echo)", got["id"])
	}
	if got["name"] != "Rex" {
		t.Errorf("name = %v, want Rex (schema example)", got["name"])
	}
}

func TestMockWriteEchoesRequestBody(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()
	rec := do(t, h, "POST", "/pets", `{"name":"Zed","tag":"x"}`, nil)
	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	got := decode(t, rec)
	if got["name"] != "Zed" {
		t.Errorf("name = %v, want echoed Zed", got["name"])
	}
	if got["tag"] != "x" {
		t.Errorf("tag = %v, want echoed x", got["tag"])
	}
}

func TestMockStatefulLifecycle(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()

	// Create a pet; the mock assigns an id and stores it.
	rec := do(t, h, "POST", "/pets", `{"name":"Luna"}`, nil)
	if rec.Code != 201 {
		t.Fatalf("create status = %d, want 201", rec.Code)
	}
	created := decode(t, rec)
	idNum, ok := created["id"].(float64)
	if !ok || idNum == 0 {
		t.Fatalf("create should assign a non-zero id, got %v", created["id"])
	}
	if created["name"] != "Luna" {
		t.Errorf("created name = %v, want Luna", created["name"])
	}

	// Read it back by the assigned id — the in-memory store must serve it.
	rec = do(t, h, "GET", "/pets/"+strconv.FormatInt(int64(idNum), 10), "", nil)
	if rec.Code != 200 {
		t.Fatalf("read-back status = %d, want 200", rec.Code)
	}
	got := decode(t, rec)
	if got["name"] != "Luna" {
		t.Errorf("read-back name = %v, want Luna (from store)", got["name"])
	}
	if got["id"] != idNum {
		t.Errorf("read-back id = %v, want %v", got["id"], idNum)
	}

	// An empty required field is rejected with the documented 4xx.
	rec = do(t, h, "POST", "/pets", `{"name":""}`, nil)
	if rec.Code != 400 {
		t.Fatalf("invalid create status = %d, want 400", rec.Code)
	}
	got = decode(t, rec)
	if got["code"] != float64(400) {
		t.Errorf("error body code = %v, want 400", got["code"])
	}
	if msg, _ := got["message"].(string); !strings.Contains(msg, "name") {
		t.Errorf("error body message %q should mention the offending field", msg)
	}

	// A missing required field is rejected too.
	rec = do(t, h, "POST", "/pets", `{"id":1}`, nil)
	if rec.Code != 400 {
		t.Errorf("missing-field create status = %d, want 400", rec.Code)
	}

	// A second create gets a distinct id.
	rec = do(t, h, "POST", "/pets", `{"name":"Max"}`, nil)
	if decode(t, rec)["id"] == idNum {
		t.Error("second create reused the first id")
	}
}

func TestMockStatusOverride(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()
	rec := do(t, h, "POST", "/pets", `{}`, map[string]string{"X-Mock-Status": "400"})
	if rec.Code != 400 {
		t.Fatalf("override status = %d, want 400", rec.Code)
	}
	got := decode(t, rec)
	if _, ok := got["message"]; !ok {
		t.Errorf("400 body should follow Error schema: %v", got)
	}

	// Undocumented override -> 400 with a helpful error.
	rec = do(t, h, "POST", "/pets", `{}`, map[string]string{"X-Mock-Status": "599"})
	if rec.Code != 400 {
		t.Errorf("undocumented override status = %d, want 400", rec.Code)
	}
}

func TestMockExplicitExampleAndNoBody(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()
	rec := do(t, h, "GET", "/health", "", nil)
	got := decode(t, rec)
	if got["status"] != "up" {
		t.Errorf("explicit example not used: %v", got)
	}

	// 204 must have no body.
	rec = do(t, h, "DELETE", "/pets/1", "", nil)
	if rec.Code != 204 {
		t.Fatalf("DELETE status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 response should have empty body, got %q", rec.Body.String())
	}
}

func TestMockNotFoundAndMethodNotAllowed(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()
	rec := do(t, h, "GET", "/nope", "", nil)
	if rec.Code != 404 {
		t.Errorf("unknown path status = %d, want 404", rec.Code)
	}

	rec = do(t, h, "PUT", "/pets/1", `{}`, nil)
	if rec.Code != 405 {
		t.Errorf("wrong method status = %d, want 405", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, "GET") || !strings.Contains(allow, "DELETE") {
		t.Errorf("Allow header = %q, want GET and DELETE", allow)
	}
}

func TestMockCORS(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	h := srv.Handler()
	rec := do(t, h, "OPTIONS", "/pets", "", nil)
	if rec.Code != 204 {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("missing CORS origin header")
	}
}

func TestMockRoutesSorted(t *testing.T) {
	srv := New(newMockDoc(t), Options{})
	routes := srv.Routes()
	if len(routes) != 5 {
		t.Fatalf("routes = %d, want 5", len(routes))
	}
	for i := 1; i < len(routes); i++ {
		if routes[i-1].Path > routes[i].Path {
			t.Errorf("routes not sorted: %v before %v", routes[i-1], routes[i])
		}
	}
}

func TestMuxPattern(t *testing.T) {
	cases := []struct {
		method, path, want string
		wantNames          []string
	}{
		{"get", "/pets", "GET /pets", nil},
		{"GET", "/pets/{id}", "GET /pets/{p0}", []string{"id"}},
		{"delete", "/pets/{petId}/toys/{toyId}", "DELETE /pets/{p0}/toys/{p1}", []string{"petId", "toyId"}},
		{"GET", "/", "GET /{$}", nil},
	}
	for _, c := range cases {
		got, names := muxPattern(c.method, c.path)
		if got != c.want {
			t.Errorf("muxPattern(%q,%q) = %q, want %q", c.method, c.path, got, c.want)
		}
		if strings.Join(names, ",") != strings.Join(c.wantNames, ",") {
			t.Errorf("names = %v, want %v", names, c.wantNames)
		}
	}
}

// --- GraphQL ------------------------------------------------------------------

const testSDL = `
type Pet {
  id: ID!
  name: String!
  tag: String
}

type Query {
  pets: [Pet!]!
  pet(id: ID!): Pet
}

type Mutation {
  createPet(name: String!): Pet!
}
`

func newGQLMock(t *testing.T) *Server {
	t.Helper()
	s, err := gql.Parse(testSDL)
	if err != nil {
		t.Fatal(err)
	}
	return New(newMockDoc(t), Options{GraphQL: s})
}

func TestGraphQLQueryHappyPath(t *testing.T) {
	srv := newGQLMock(t)
	h := srv.Handler()

	body := `{"query": "{ pets { id name tag } }"}`
	rec := do(t, h, "POST", "/graphql", body, nil)
	if rec.Code != 200 {
		t.Fatalf("POST /graphql status = %d, body %s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing data: %v", got)
	}
	pets, ok := data["pets"].([]any)
	if !ok || len(pets) != 1 {
		t.Fatalf("pets should be a one-item list, got %v", data["pets"])
	}
	pet := pets[0].(map[string]any)
	if pet["id"] != "1" || pet["name"] != "string" {
		t.Errorf("pet = %v", pet)
	}
}

func TestGraphQLErrorEnvelope(t *testing.T) {
	srv := newGQLMock(t)
	h := srv.Handler()

	// Unknown field -> GraphQL error envelope, still HTTP 200.
	rec := do(t, h, "POST", "/graphql", `{"query": "{ nope }"}`, nil)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (GraphQL convention)", rec.Code)
	}
	got := decode(t, rec)
	errs, ok := got["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("want errors in envelope, got %v", got)
	}
	msg := errs[0].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "Cannot query field") {
		t.Errorf("error message = %q", msg)
	}

	// Missing query field -> 400 with a helpful message.
	rec = do(t, h, "POST", "/graphql", `{"variables": {}}`, nil)
	if rec.Code != 400 {
		t.Errorf("empty query status = %d, want 400", rec.Code)
	}
}

func TestGraphQLOperationNameSelection(t *testing.T) {
	s, err := gql.Parse(`
		type Query {
			a: String
			b: Int
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(newMockDoc(t), Options{GraphQL: s})
	h := srv.Handler()

	query := `query First { a } query Second { b }`
	rec := do(t, h, "POST", "/graphql", fmt.Sprintf(`{"query": %q, "operationName": "Second"}`, query), nil)
	got := decode(t, rec)
	data := got["data"].(map[string]any)
	if _, ok := data["b"]; !ok {
		t.Errorf("expected operation Second selected, got %v", data)
	}
	if _, ok := data["a"]; ok {
		t.Errorf("operation First should not be present: %v", data)
	}
}

func TestGraphQLMockStatusOverride(t *testing.T) {
	srv := newGQLMock(t)
	h := srv.Handler()

	rec := do(t, h, "POST", "/graphql", `{"query": "{ pets { id } }"}`, map[string]string{"X-Mock-Status": "500"})
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 (X-Mock-Status override)", rec.Code)
	}
	// Envelope is preserved so clients can still parse it.
	got := decode(t, rec)
	if _, ok := got["data"]; !ok {
		t.Errorf("envelope should keep data key: %v", got)
	}
}

func TestGraphQLIndexAndNotFound(t *testing.T) {
	srv := newGQLMock(t)
	h := srv.Handler()

	// Index should list the graphql route.
	rec := do(t, h, "GET", "/", "", nil)
	idx := decode(t, rec)
	ops := idx["operations"].([]any)
	found := false
	for _, o := range ops {
		m := o.(map[string]any)
		if m["method"] == "POST" && m["path"] == "/graphql" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("index should list POST /graphql: %v", ops)
	}

	// GET /graphql (wrong method) -> 405.
	rec = do(t, h, "GET", "/graphql", "", nil)
	if rec.Code != 405 {
		t.Errorf("GET /graphql status = %d, want 405", rec.Code)
	}
}

// --- auth enforcement ---------------------------------------------------------

// newSecureDoc builds a document with an apiKey scheme, a bearer scheme, and
// one operation per auth shape (plus a public one).
func newSecureDoc(t *testing.T) *spec.Document {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Secure API
  version: 1.0.0
paths:
  /public:
    get:
      operationId: getPublic
      summary: Public data
      description: No auth required.
      responses:
        "200":
          description: OK
  /secure/key:
    get:
      operationId: getKeyData
      summary: ApiKey-protected data
      description: Requires the X-API-Key header.
      security:
        - ApiKeyAuth: []
      responses:
        "200":
          description: OK
        "401":
          description: Unauthorized
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
  /secure/bearer:
    get:
      operationId: getBearerData
      summary: Bearer-protected data
      description: Requires Authorization Bearer.
      security:
        - BearerAuth: []
      responses:
        "200":
          description: OK
        "403":
          description: Forbidden
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
  /secure/undocumented:
    get:
      operationId: getUndocumented
      summary: Secured but no 401 documented
      description: Auth required, error shape not documented.
      security:
        - ApiKeyAuth: []
      responses:
        "200":
          description: OK
components:
  securitySchemes:
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key
    BearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
  schemas:
    Error:
      type: object
      properties:
        code:
          type: integer
        message:
          type: string
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

func TestMockAuthEnforcement(t *testing.T) {
	srv := New(newSecureDoc(t), Options{})
	h := srv.Handler()

	// Public endpoint: no credentials needed.
	if rec := do(t, h, "GET", "/public", "", nil); rec.Code != 200 {
		t.Errorf("public status = %d, want 200", rec.Code)
	}

	// apiKey: missing -> documented 401 with the Error schema shape.
	rec := do(t, h, "GET", "/secure/key", "", nil)
	if rec.Code != 401 {
		t.Errorf("apiKey missing: status = %d, want 401", rec.Code)
	}
	body := decode(t, rec)
	if _, hasCode := body["code"]; !hasCode {
		t.Errorf("401 body should follow the documented Error schema: %v", body)
	}

	// apiKey: present -> 200.
	rec = do(t, h, "GET", "/secure/key", "", map[string]string{"X-API-Key": "test-key-123"})
	if rec.Code != 200 {
		t.Errorf("apiKey present: status = %d, want 200", rec.Code)
	}

	// bearer: missing -> documented 403.
	rec = do(t, h, "GET", "/secure/bearer", "", nil)
	if rec.Code != 403 {
		t.Errorf("bearer missing: status = %d, want 403", rec.Code)
	}

	// bearer: wrong scheme (Basic) still fails.
	rec = do(t, h, "GET", "/secure/bearer", "", map[string]string{"Authorization": "Basic dXNlcjpwdw=="})
	if rec.Code != 403 {
		t.Errorf("basic instead of bearer: status = %d, want 403", rec.Code)
	}

	// bearer: well-formed token -> 200 (value never validated).
	rec = do(t, h, "GET", "/secure/bearer", "", map[string]string{"Authorization": "Bearer any.token.here"})
	if rec.Code != 200 {
		t.Errorf("bearer present: status = %d, want 200", rec.Code)
	}

	// Secured op without a documented 401/403 gets a synthetic 401.
	rec = do(t, h, "GET", "/secure/undocumented", "", nil)
	if rec.Code != 401 {
		t.Errorf("undocumented auth error: status = %d, want synthetic 401", rec.Code)
	}
	if msg, _ := decode(t, rec)["error"].(string); !strings.Contains(msg, "credentials") {
		t.Errorf("synthetic 401 message = %q", msg)
	}
}

func TestMockAuthDocumentLevelAndPublicOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Global Auth API
  version: 1.0.0
security:
  - ApiKeyAuth: []
paths:
  /everything:
    get:
      operationId: getEverything
      summary: Inherits document security
      description: Protected by the document-level requirement.
      responses:
        "200":
          description: OK
  /health:
    get:
      operationId: getHealth
      summary: Health check
      description: Explicitly public despite the document default.
      security: []
      responses:
        "200":
          description: OK
components:
  securitySchemes:
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
	h := New(doc, Options{}).Handler()

	// Document-level security applies to operations without their own.
	if rec := do(t, h, "GET", "/everything", "", nil); rec.Code != 401 {
		t.Errorf("inherits document security: status = %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/everything", "", map[string]string{"X-API-Key": "k"}); rec.Code != 200 {
		t.Errorf("with key: status = %d, want 200", rec.Code)
	}

	// `security: []` opts an operation out of the document default.
	if rec := do(t, h, "GET", "/health", "", nil); rec.Code != 200 {
		t.Errorf("explicitly public: status = %d, want 200", rec.Code)
	}
}

func TestMockAuthQueryAndCookieAPIKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Key Locations API
  version: 1.0.0
paths:
  /by-query:
    get:
      operationId: getByQuery
      summary: Query param key
      description: Uses an api key in the query string.
      security:
        - QueryKey: []
      responses:
        "200":
          description: OK
  /by-cookie:
    get:
      operationId: getByCookie
      summary: Cookie key
      description: Uses an api key in a cookie.
      security:
        - CookieKey: []
      responses:
        "200":
          description: OK
components:
  securitySchemes:
    QueryKey:
      type: apiKey
      in: query
      name: api_key
    CookieKey:
      type: apiKey
      in: cookie
      name: session
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := spec.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h := New(doc, Options{}).Handler()

	if rec := do(t, h, "GET", "/by-query", "", nil); rec.Code != 401 {
		t.Errorf("query key missing: status = %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/by-query", "", nil); rec.Code != 401 {
		t.Errorf("query key still missing: status = %d, want 401", rec.Code)
	}
	// With the query key present (simulated via a raw request).
	req := httptest.NewRequest("GET", "/by-query?api_key=abc", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("query key present: status = %d, want 200", rec.Code)
	}

	// Cookie.
	req = httptest.NewRequest("GET", "/by-cookie", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "s3cr3t"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("cookie present: status = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "GET", "/by-cookie", "", nil); rec.Code != 401 {
		t.Errorf("cookie missing: status = %d, want 401", rec.Code)
	}
}

func TestMockAuthORAndANDSemantics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	content := `openapi: 3.1.0
info:
  title: Combinator API
  version: 1.0.0
paths:
  /either:
    get:
      operationId: getEither
      summary: Either credential works
      description: Two requirements (OR between list items).
      security:
        - ApiKeyAuth: []
        - BearerAuth: []
      responses:
        "200":
          description: OK
  /both:
    get:
      operationId: getBoth
      summary: Both credentials required
      description: One requirement naming two schemes (AND within the item).
      security:
        - ApiKeyAuth: []
          BearerAuth: []
      responses:
        "200":
          description: OK
components:
  securitySchemes:
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key
    BearerAuth:
      type: http
      scheme: bearer
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := spec.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h := New(doc, Options{}).Handler()

	// OR: each requirement alone grants access.
	if rec := do(t, h, "GET", "/either", "", map[string]string{"X-API-Key": "k"}); rec.Code != 200 {
		t.Errorf("OR api key only: status = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "GET", "/either", "", map[string]string{"Authorization": "Bearer t"}); rec.Code != 200 {
		t.Errorf("OR bearer only: status = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "GET", "/either", "", nil); rec.Code != 401 {
		t.Errorf("OR neither: status = %d, want 401", rec.Code)
	}

	// AND: every scheme in one requirement must be present.
	if rec := do(t, h, "GET", "/both", "", map[string]string{"X-API-Key": "k"}); rec.Code != 401 {
		t.Errorf("AND api key only: status = %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/both", "", map[string]string{"Authorization": "Bearer t"}); rec.Code != 401 {
		t.Errorf("AND bearer only: status = %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/both", "", map[string]string{"X-API-Key": "k", "Authorization": "Bearer t"}); rec.Code != 200 {
		t.Errorf("AND both: status = %d, want 200", rec.Code)
	}
}
