// Package mock serves deterministic mock responses for every operation
// documented in an OpenAPI document, so clients and test scenarios can run
// before the real API exists.
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bilhasry/merapi/internal/docs"
	"github.com/bilhasry/merapi/internal/gql"
	"github.com/bilhasry/merapi/internal/spec"
)

// Options configures a mock server.
type Options struct {
	// Log receives one line per request; nil disables logging.
	Log io.Writer
	// Delay is an artificial latency applied to every response.
	Delay time.Duration
	// GraphQL, when non-nil, adds POST /graphql served from the SDL schema.
	GraphQL *gql.Schema
}

// Route describes one mocked operation.
type Route struct {
	Method      string
	Path        string
	OperationID string
	Status      int // default status code returned
}

// Server mocks every documented operation of an OpenAPI document.
type Server struct {
	doc    *spec.Document
	opts   Options
	routes []Route
	logger *log.Logger

	// In-memory resource store so write→read lifecycle tests pass against the
	// mock: successful creates/updates persist under their concrete path, and
	// item GETs/DELETEs read/evict from it. Guarded by mu.
	mu    sync.Mutex
	store map[string]any
	seq   int
}

// New builds a mock server for every operation in the document.
func New(doc *spec.Document, opts Options) *Server {
	s := &Server{doc: doc, opts: opts, store: map[string]any{}}
	if opts.Log != nil {
		s.logger = log.New(opts.Log, "", 0)
	}
	for _, sum := range doc.ListEndpoints(spec.EndpointFilter{}) {
		ep := doc.GetEndpoint(sum.Method, sum.Path)
		if ep == nil {
			continue
		}
		status, _ := primaryResponse(ep)
		s.routes = append(s.routes, Route{
			Method:      strings.ToUpper(ep.Method),
			Path:        ep.Path,
			OperationID: ep.OperationID,
			Status:      status,
		})
	}
	return s
}

// Routes lists the mocked operations (sorted by path, then method).
func (s *Server) Routes() []Route {
	out := append([]Route{}, s.routes...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// Handler returns the mock HTTP handler (routes registered with Go 1.22+
// method+wildcard patterns, so /pets/{id} style paths match naturally).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	reg := map[string]bool{}
	for _, sum := range s.doc.ListEndpoints(spec.EndpointFilter{}) {
		ep := s.doc.GetEndpoint(sum.Method, sum.Path)
		if ep == nil {
			continue
		}
		pattern, names := muxPattern(ep.Method, ep.Path)
		if reg[pattern] {
			continue // identical pattern already registered
		}
		reg[pattern] = true
		mux.HandleFunc(pattern, s.operationHandler(ep, names))
	}
	if s.opts.GraphQL != nil {
		mux.HandleFunc("POST /graphql", s.graphqlHandler)
	}
	mux.HandleFunc("GET /{$}", s.indexHandler)
	mux.HandleFunc("/", s.notFoundHandler)
	return s.logMiddleware(s.corsMiddleware(mux))
}

// Run serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

// muxPattern converts an OpenAPI method+path into a ServeMux pattern.
// Path parameters become positional wildcards ({p0}, {p1}, ...) so that
// operations using different parameter names on similar shapes never conflict;
// the original names are returned for value extraction.
func muxPattern(method, path string) (string, []string) {
	var names []string
	var b strings.Builder
	b.WriteString(strings.ToUpper(method))
	b.WriteString(" ")
	for {
		open := strings.Index(path, "{")
		if open < 0 {
			b.WriteString(path)
			break
		}
		close := strings.Index(path[open:], "}")
		if close < 0 {
			b.WriteString(path) // malformed; treat literally
			break
		}
		close += open
		b.WriteString(path[:open])
		fmt.Fprintf(&b, "{p%d}", len(names))
		names = append(names, path[open+1:close])
		path = path[close+1:]
	}
	if strings.HasSuffix(b.String(), "/") {
		// ServeMux patterns must not end with a slash (except root).
		p := strings.TrimSuffix(b.String(), "/")
		return p + "/{$}", names
	}
	if b.String() == strings.ToUpper(method)+" /" {
		return strings.ToUpper(method) + " /{$}", names
	}
	return b.String(), names
}

// operationHandler answers one documented operation.
func (s *Server) operationHandler(ep *spec.Endpoint, paramNames []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pathParams := map[string]string{}
		for i, name := range paramNames {
			pathParams[name] = r.PathValue(fmt.Sprintf("p%d", i))
		}

		// Enforce documented security requirements before anything else: a
		// request without credentials answers with the documented 401/403
		// (shape-only check — the mock never validates token values).
		if msg := s.authError(ep, r); msg != "" {
			status, resp, ok := pickAuthError(ep)
			if !ok {
				status, resp = http.StatusUnauthorized, spec.Response{}
			}
			body, hasBody := s.errorBody(resp, status, msg)
			s.respond(w, r, status, resp, body, hasBody)
			s.logf("%-6s %-30s -> %d  %s (auth)", r.Method, r.URL.Path, status, ep.OperationID)
			return
		}

		status, resp := pickResponse(ep, r.Header.Get("X-Mock-Status"))
		if status == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("status %s is not documented for %s %s",
					r.Header.Get("X-Mock-Status"), ep.Method, ep.Path),
			})
			return
		}

		// Parse the request body once for write operations.
		var reqBody any
		if isWriteMethod(r.Method) {
			if data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(data) > 0 {
				_ = json.Unmarshal(data, &reqBody)
			}
		}

		// Enforce the schema's required list so error paths can be exercised
		// without a real backend (e.g. POST with an empty required field).
		if msg := s.validateWrite(ep, r.Method, reqBody); msg != "" {
			if code, errResp, ok := pickError(ep); ok {
				errBody, hasErr := s.errorBody(errResp, code, msg)
				s.respond(w, r, code, errResp, errBody, hasErr)
				return
			}
		}

		body, hasBody := s.buildBody(ep, resp, r.Method, reqBody, pathParams)

		// In-memory store so create→read lifecycle tests pass against the mock.
		switch {
		case isReadMethod(r.Method) && len(paramNames) > 0:
			if stored, ok := s.lookup(r.URL.Path); ok {
				body, hasBody = stored, true
			}
		case isWriteMethod(r.Method) && status >= 200 && status < 300:
			s.persist(ep, r.Method, r.URL.Path, paramNames, body)
		case r.Method == http.MethodDelete && len(paramNames) > 0:
			s.evict(r.URL.Path)
		}

		s.respond(w, r, status, resp, body, hasBody)
		s.logf("%-6s %-30s -> %d  %s", r.Method, r.URL.Path, status, ep.OperationID)
	}
}

// graphqlHandler serves POST /graphql from the SDL schema: it parses
// {query, variables, operationName}, builds a deterministic example response,
// and honors X-Mock-Status for error-path exploration (status must be 200 to
// keep the GraphQL envelope).
func (s *Server) graphqlHandler(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req struct {
		Query         string         `json:"query"`
		Variables     map[string]any `json:"variables"`
		OperationName string         `json:"operationName"`
	}
	if err := json.Unmarshal(data, &req); err != nil || strings.TrimSpace(req.Query) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"errors": []map[string]any{{"message": "body must be JSON with a non-empty \"query\" field"}},
		})
		return
	}
	body, status := s.opts.GraphQL.Example(req.Query, req.OperationName)
	// X-Mock-Status forces a documented status (e.g. 500) for error paths;
	// the GraphQL envelope is preserved so clients can parse errors.
	if code := r.Header.Get("X-Mock-Status"); code != "" {
		if n, err := strconv.Atoi(code); err == nil && n >= 100 && n <= 599 {
			status = n
		}
	}
	writeJSON(w, status, body)
	s.logf("%-6s %-30s -> %d  graphql", r.Method, r.URL.Path, status)
}

// errorBody builds a payload for a documented error response, injecting the
// status code and message when the shape has room for them.
func (s *Server) errorBody(resp spec.Response, status int, msg string) (any, bool) {
	base, has := s.buildBody(&spec.Endpoint{}, resp, "", nil, nil)
	if !has {
		return map[string]any{"error": msg}, true
	}
	if obj, ok := base.(map[string]any); ok {
		if _, exists := obj["code"]; exists {
			obj["code"] = status
		}
		if _, exists := obj["message"]; exists {
			obj["message"] = msg
		}
		if _, exists := obj["error"]; !exists && len(obj) == 0 {
			obj["error"] = msg
		}
	}
	return base, true
}

// respond writes a response with the endpoint's content type (or JSON).
func (s *Server) respond(w http.ResponseWriter, r *http.Request, status int, resp spec.Response, body any, hasBody bool) {
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	} else if hasBody {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if hasBody {
		_ = json.NewEncoder(w).Encode(body)
	}
}

// lookup fetches a stored resource by its concrete path.
func (s *Server) lookup(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.store[key]
	return v, ok
}

// evict removes a stored resource (DELETE).
func (s *Server) evict(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, key)
}

// persist stores a created/updated resource so later item reads find it. A
// POST to a collection assigns an id and stores under the sibling item path.
func (s *Server) persist(ep *spec.Endpoint, method, path string, paramNames []string, body any) {
	obj, ok := body.(map[string]any)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if method == http.MethodPost && len(paramNames) == 0 {
		if isZeroID(obj["id"]) {
			s.seq++
			obj["id"] = s.seq
		}
		if tmpl, paramName, found := s.itemTemplate(path); found {
			key := strings.Replace(tmpl, "{"+paramName+"}", fmt.Sprint(obj["id"]), 1)
			s.store[key] = obj
		}
		return
	}
	// Update (PUT/PATCH on an item) or a POST aimed straight at an item path.
	s.store[path] = obj
}

// itemTemplate finds a sibling route shaped "<collection>/{param}" so a
// collection POST knows where to store the created resource.
func (s *Server) itemTemplate(collectionPath string) (string, string, bool) {
	prefix := strings.TrimSuffix(collectionPath, "/")
	for _, rt := range s.routes {
		p := rt.Path
		if strings.HasPrefix(p, prefix+"/{") && strings.HasSuffix(p, "}") &&
			strings.Count(p, "/") == strings.Count(prefix, "/")+1 {
			param := strings.TrimSuffix(strings.TrimPrefix(p, prefix+"/"), "}")
			param = strings.TrimPrefix(param, "{")
			return p, param, true
		}
	}
	return "", "", false
}

func isZeroID(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case int:
		return t == 0
	case float64:
		return t == 0
	case string:
		return t == ""
	default:
		return false
	}
}

// buildBody derives the response payload: the documented example verbatim,
// or a minimal example generated from the response schema. For write
// operations the request body is echoed over the generated shape, and path
// parameters fill matching object properties (e.g. GET /pets/42 -> id: 42).
func (s *Server) buildBody(ep *spec.Endpoint, resp spec.Response, method string, reqBody any, pathParams map[string]string) (any, bool) {
	if resp.Example != nil {
		return resp.Example, true
	}
	var schema map[string]any
	switch {
	case resp.SchemaRef != "":
		schema = s.doc.GetSchema(refName(resp.SchemaRef))
	case resp.Schema != nil:
		schema = resp.Schema
	}
	base := docs.ExampleFromSchema(s.doc, schema)

	if isWriteMethod(method) {
		base = overlay(base, reqBody)
	}
	if obj, ok := base.(map[string]any); ok {
		for name, value := range pathParams {
			if _, exists := obj[name]; exists {
				obj[name] = coerce(value)
			}
		}
	}
	if base == nil {
		return nil, false
	}
	return base, true
}

// pickResponse chooses the response code to mock: the explicit X-Mock-Status
// override when documented, else the lowest 2xx (fallback: first documented).
// A zero status means the requested override is not documented.
func pickResponse(ep *spec.Endpoint, override string) (int, spec.Response) {
	codes := make([]int, 0, len(ep.Responses))
	byCode := map[int]spec.Response{}
	for key, resp := range ep.Responses {
		code, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			continue
		}
		codes = append(codes, code)
		byCode[code] = resp
	}
	sort.Ints(codes)

	if override != "" {
		code, err := strconv.Atoi(override)
		if err == nil {
			if resp, ok := byCode[code]; ok {
				return code, resp
			}
			return 0, spec.Response{}
		}
	}
	for _, code := range codes {
		if code >= 200 && code < 300 {
			return code, byCode[code]
		}
	}
	if len(codes) == 0 {
		return http.StatusOK, spec.Response{}
	}
	return codes[0], byCode[codes[0]]
}

// primaryResponse reports the default mocked status of an operation.
func primaryResponse(ep *spec.Endpoint) (int, spec.Response) {
	return pickResponse(ep, "")
}

// pickError finds the lowest documented 4xx/5xx response, so validation
// failures can answer with a documented error shape. ok is false when the
// operation documents no error response.
func pickError(ep *spec.Endpoint) (int, spec.Response, bool) {
	var codes []int
	byCode := map[int]spec.Response{}
	for key, resp := range ep.Responses {
		code, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || code < 400 {
			continue
		}
		codes = append(codes, code)
		byCode[code] = resp
	}
	if len(codes) == 0 {
		return 0, spec.Response{}, false
	}
	sort.Ints(codes)
	return codes[0], byCode[codes[0]], true
}

// requestSchema resolves the request-body schema (component ref or inline).
func (s *Server) requestSchema(ep *spec.Endpoint) map[string]any {
	if ep.RequestBody == nil {
		return nil
	}
	if ep.RequestBody.SchemaRef != "" {
		return s.doc.GetSchema(refName(ep.RequestBody.SchemaRef))
	}
	return ep.RequestBody.Schema
}

// validateWrite enforces the schema's `required` list on a write body: a
// missing field or an empty string is rejected. It returns a human-readable
// message when invalid. Reads and bodies without a schema always pass.
func (s *Server) validateWrite(ep *spec.Endpoint, method string, reqBody any) string {
	if !isWriteMethod(method) {
		return ""
	}
	schema := s.requestSchema(ep)
	if schema == nil {
		return ""
	}
	obj, ok := reqBody.(map[string]any)
	if !ok {
		if reqBody == nil {
			return "request body is required"
		}
		return "" // non-object bodies (lists, scalars) aren't checked here
	}
	required, _ := schema["required"].([]any)
	for _, rf := range required {
		name, _ := rf.(string)
		if name == "" {
			continue
		}
		v, present := obj[name]
		if !present || v == nil {
			return fmt.Sprintf("field %q is required", name)
		}
		if str, isStr := v.(string); isStr && strings.TrimSpace(str) == "" {
			return fmt.Sprintf("field %q must not be empty", name)
		}
	}
	return ""
}

// overlay merges the request body over the generated base (request wins).
func overlay(base, reqBody any) any {
	bm, ok := base.(map[string]any)
	if !ok {
		if base == nil {
			return reqBody
		}
		return base // documented array/scalar shape stays authoritative
	}
	rm, ok := reqBody.(map[string]any)
	if !ok {
		return bm
	}
	out := map[string]any{}
	for k, v := range bm {
		out[k] = v
	}
	for k, v := range rm {
		out[k] = v
	}
	return out
}

// coerce turns a path parameter string into an int when possible.
func coerce(v string) any {
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return i
	}
	return v
}

func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

func isReadMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead:
		return true
	}
	return false
}

func (s *Server) indexHandler(w http.ResponseWriter, r *http.Request) {
	type op struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Status int    `json:"status"`
	}
	ops := make([]op, 0, len(s.routes))
	for _, rt := range s.Routes() {
		ops = append(ops, op{rt.Method, rt.Path, rt.Status})
	}
	if s.opts.GraphQL != nil {
		ops = append(ops, op{Method: "POST", Path: "/graphql", Status: 200})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":    s.doc.Title(),
		"version":    s.doc.Version(),
		"mock":       "merapi",
		"operations": ops,
		"hint":       "send X-Mock-Status: <code> to receive another documented response",
	})
}

func (s *Server) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	// GraphQL endpoint: distinguish wrong method from undocumented path.
	if s.opts.GraphQL != nil && r.URL.Path == "/graphql" {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": fmt.Sprintf("%s is not allowed; use POST /graphql", r.Method),
		})
		return
	}
	// Distinguish "path exists, wrong method" from "undocumented path".
	for _, rt := range s.routes {
		if strings.EqualFold(rt.Method, r.Method) {
			continue // a same-method route would have been handled already
		}
		pattern, _ := muxPattern(rt.Method, rt.Path)
		if ok, _ := matchPattern(pattern, rt.Method, r.URL.Path); ok {
			w.Header().Set("Allow", allowedMethods(s.routes, r.URL.Path))
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
				"error": fmt.Sprintf("%s is not allowed; documented methods: %s",
					r.Method, allowedMethods(s.routes, r.URL.Path)),
			})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": fmt.Sprintf("no mock for %s %s — document it with `merapi endpoint add`", r.Method, r.URL.Path),
	})
}

// allowedMethods lists the documented methods for a concrete request path.
func allowedMethods(routes []Route, path string) string {
	var out []string
	for _, rt := range routes {
		pattern, _ := muxPattern(rt.Method, rt.Path)
		if ok, _ := matchPattern(pattern, rt.Method, path); ok {
			out = append(out, rt.Method)
		}
	}
	return strings.Join(out, ", ")
}

// matchPattern reports whether method+path satisfy a ServeMux-style pattern
// built by muxPattern (literal segments and {pN} wildcards only).
func matchPattern(pattern, method, path string) (bool, map[string]string) {
	pm, pat, ok := strings.Cut(pattern, " ")
	if !ok || !strings.EqualFold(pm, method) {
		return false, nil
	}
	pSegs := strings.Split(strings.Trim(pat, "/"), "/")
	sSegs := strings.Split(strings.Trim(path, "/"), "/")
	if strings.Trim(pat, "/") == "" && strings.Trim(path, "/") == "" {
		return true, nil
	}
	if len(pSegs) != len(sSegs) {
		return false, nil
	}
	params := map[string]string{}
	for i, seg := range pSegs {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			params[seg[1:len(seg)-1]] = sSegs[i]
			continue
		}
		if seg != sSegs[i] {
			return false, nil
		}
	}
	return true, params
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Delay > 0 {
			time.Sleep(s.opts.Delay)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}
