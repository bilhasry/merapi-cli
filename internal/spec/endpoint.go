package spec

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Methods lists the HTTP methods managed as operations, in display order.
var Methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Parameter mirrors an OpenAPI parameter object (subset).
type Parameter struct {
	Name        string         `json:"name" yaml:"name"`
	In          string         `json:"in" yaml:"in"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool           `json:"required,omitempty" yaml:"required,omitempty"`
	Example     string         `json:"example,omitempty" yaml:"example,omitempty"`
	Schema      map[string]any `json:"schema,omitempty" yaml:"schema,omitempty"`
}

// RequestBody mirrors an OpenAPI requestBody object (subset).
type RequestBody struct {
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool           `json:"required,omitempty" yaml:"required,omitempty"`
	ContentType string         `json:"contentType,omitempty" yaml:"contentType,omitempty"`
	SchemaRef   string         `json:"schemaRef,omitempty" yaml:"schemaRef,omitempty"`
	Schema      map[string]any `json:"schema,omitempty" yaml:"schema,omitempty"`
	Example     any            `json:"example,omitempty" yaml:"example,omitempty"`
}

// Response mirrors an OpenAPI response object (subset).
type Response struct {
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	ContentType string         `json:"contentType,omitempty" yaml:"contentType,omitempty"`
	SchemaRef   string         `json:"schemaRef,omitempty" yaml:"schemaRef,omitempty"`
	Schema      map[string]any `json:"schema,omitempty" yaml:"schema,omitempty"`
	Example     any            `json:"example,omitempty" yaml:"example,omitempty"`
}

// Endpoint is the editable view of one operation in the document.
type Endpoint struct {
	Method      string              `json:"method" yaml:"method"`
	Path        string              `json:"path" yaml:"path"`
	OperationID string              `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	Summary     string              `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description string              `json:"description,omitempty" yaml:"description,omitempty"`
	Tags        []string            `json:"tags,omitempty" yaml:"tags,omitempty"`
	Deprecated  bool                `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"requestBody,omitempty" yaml:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses,omitempty" yaml:"responses,omitempty"`
	// Security is the per-operation requirement list (OR between items,
	// scheme names with optional scopes inside one item). Nil = inherit the
	// document-level security; empty = explicitly public.
	Security []map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
}

// EndpointSummary is a compact listing entry.
type EndpointSummary struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	OperationID string   `json:"operationId,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Responses   []string `json:"responses,omitempty"`
	HasBody     bool     `json:"hasRequestBody"`
}

// EndpointFilter narrows endpoint listings.
type EndpointFilter struct {
	PathPrefix string
	Tag        string
}

// operationNode returns the operation mapping for method+path, or nil.
func (d *Document) operationNode(method, path string) *yaml.Node {
	item := mapGet(d.PathsNode(), path)
	if item == nil {
		return nil
	}
	return mapGet(item, strings.ToLower(method))
}

// pathItemNode returns (creating if needed) the path item for a path.
func (d *Document) pathItemNode(path string) *yaml.Node {
	return mapGetOrCreate(d.PathsNode(), path)
}

// GetEndpoint decodes a full endpoint, or returns nil when not found.
func (d *Document) GetEndpoint(method, path string) *Endpoint {
	op := d.operationNode(method, path)
	if op == nil {
		return nil
	}
	m := strings.ToLower(method)
	ep := &Endpoint{
		Method:      m,
		Path:        path,
		OperationID: nodeValue(op, "operationId"),
		Summary:     nodeValue(op, "summary"),
		Description: nodeValue(op, "description"),
		Tags:        decodeStringSlice(mapGet(op, "tags")),
		Deprecated:  nodeValue(op, "deprecated") == "true",
	}
	if params := mapGet(op, "parameters"); params != nil && params.Kind == yaml.SequenceNode {
		for _, p := range params.Content {
			var param Parameter
			if err := p.Decode(&param); err == nil {
				ep.Parameters = append(ep.Parameters, param)
			}
		}
	}
	if rb := mapGet(op, "requestBody"); rb != nil {
		ep.RequestBody = &RequestBody{
			Description: nodeValue(rb, "description"),
			Required:    nodeValue(rb, "required") == "true",
		}
		if content := mapGet(rb, "content"); content != nil {
			for _, ct := range mapKeys(content) {
				media := mapGet(content, ct)
				ep.RequestBody.ContentType = ct
				ep.RequestBody.SchemaRef, ep.RequestBody.Schema = decodeSchema(mapGet(media, "schema"))
				if ex := mapGet(media, "example"); ex != nil {
					if v, err := anyFromNode(ex); err == nil {
						ep.RequestBody.Example = v
					}
				}
				break
			}
		}
	}
	if responses := mapGet(op, "responses"); responses != nil {
		ep.Responses = map[string]Response{}
		for _, code := range mapKeys(responses) {
			rnode := mapGet(responses, code)
			resp := Response{
				Description: nodeValue(rnode, "description"),
				ContentType: "",
			}
			if content := mapGet(rnode, "content"); content != nil {
				for _, ct := range mapKeys(content) {
					media := mapGet(content, ct)
					resp.ContentType = ct
					resp.SchemaRef, resp.Schema = decodeSchema(mapGet(media, "schema"))
					if ex := mapGet(media, "example"); ex != nil {
						if v, err := anyFromNode(ex); err == nil {
							resp.Example = v
						}
					}
					break
				}
			}
			ep.Responses[code] = resp
		}
	}
	if sec := mapGet(op, "security"); sec != nil && sec.Kind == yaml.SequenceNode {
		ep.Security = decodeSecurityNode(sec)
	}
	return ep
}

func nodeValue(n *yaml.Node, key string) string {
	if v := mapGet(n, key); v != nil {
		return v.Value
	}
	return ""
}

// decodeSchema turns a schema node into a component schema name (for $refs
// into components/schemas) and/or an inline schema map.
func decodeSchema(n *yaml.Node) (string, map[string]any) {
	if n == nil {
		return "", nil
	}
	if ref := mapGet(n, "$ref"); ref != nil {
		return strings.TrimPrefix(ref.Value, "#/components/schemas/"), nil
	}
	var m map[string]any
	if err := n.Decode(&m); err != nil {
		return "", nil
	}
	return "", m
}

// ListEndpoints returns endpoints in document order.
func (d *Document) ListEndpoints(filter EndpointFilter) []EndpointSummary {
	var out []EndpointSummary
	paths := d.PathsNode()
	for _, path := range mapKeys(paths) {
		if filter.PathPrefix != "" && !strings.HasPrefix(path, filter.PathPrefix) {
			continue
		}
		item := mapGet(paths, path)
		if item == nil || item.Kind != yaml.MappingNode {
			continue
		}
		for _, m := range Methods {
			op := mapGet(item, m)
			if op == nil || op.Kind != yaml.MappingNode {
				continue
			}
			ep := d.GetEndpoint(m, path)
			if ep == nil {
				continue
			}
			if filter.Tag != "" && !containsString(ep.Tags, filter.Tag) {
				continue
			}
			var codes []string
			for code := range ep.Responses {
				codes = append(codes, code)
			}
			out = append(out, EndpointSummary{
				Method:      m,
				Path:        path,
				OperationID: ep.OperationID,
				Summary:     ep.Summary,
				Description: ep.Description,
				Tags:        ep.Tags,
				Responses:   codes,
				HasBody:     ep.RequestBody != nil,
			})
		}
	}
	return out
}

// ListTags returns all distinct tags used by operations, in document order.
func (d *Document) ListTags() []string {
	seen := map[string]bool{}
	var out []string
	for _, ep := range d.ListEndpoints(EndpointFilter{}) {
		for _, t := range ep.Tags {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

func containsString(items []string, s string) bool {
	for _, item := range items {
		if item == s {
			return true
		}
	}
	return false
}

// ErrExists signals the endpoint already exists.
type ErrExists struct{ Method, Path string }

func (e *ErrExists) Error() string {
	return fmt.Sprintf("%s %s already exists (use `merapi endpoint update` or MCP tool endpoint_update)", e.Method, e.Path)
}

// ErrNotFound signals the endpoint does not exist.
type ErrNotFound struct{ Method, Path string }

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("%s %s not found in the document", strings.ToUpper(e.Method), e.Path)
}

// AddEndpoint inserts a new operation. Missing fields receive sensible
// defaults: operationId derived from method+path, tag derived from the first
// static path segment, and a 200 response when none are supplied.
func (d *Document) AddEndpoint(ep *Endpoint) error {
	method, err := normalizeMethod(ep.Method)
	if err != nil {
		return err
	}
	path, err := normalizePath(ep.Path)
	if err != nil {
		return err
	}
	ep.Method, ep.Path = method, path
	if d.operationNode(method, path) != nil {
		return &ErrExists{Method: method, Path: path}
	}
	if ep.OperationID == "" {
		ep.OperationID = GenerateOperationID(method, path)
	}
	if len(ep.Tags) == 0 {
		if t := deriveTag(path); t != "" {
			ep.Tags = []string{t}
		}
	}
	if len(ep.Responses) == 0 {
		ep.Responses = map[string]Response{"200": {Description: "Successful response"}}
	}
	op := newMapping()
	mapSet(op, "operationId", strNode(ep.OperationID))
	if ep.Summary != "" {
		mapSet(op, "summary", strNode(ep.Summary))
	}
	if ep.Description != "" {
		mapSet(op, "description", strNode(ep.Description))
	}
	if len(ep.Tags) > 0 {
		mapSet(op, "tags", seqOfStrings(ep.Tags))
	}
	if len(ep.Parameters) > 0 {
		mapSet(op, "parameters", encodeParameters(ep.Parameters))
	}
	if ep.RequestBody != nil {
		mapSet(op, "requestBody", encodeRequestBody(ep.RequestBody))
	}
	mapSet(op, "responses", encodeResponses(ep.Responses))
	if ep.Security != nil {
		mapSet(op, "security", encodeSecurity(ep.Security))
	}
	if ep.Deprecated {
		mapSet(op, "deprecated", boolNode(true))
	}
	item := d.pathItemNode(path)
	mapSet(item, strings.ToLower(method), op)
	return nil
}

// UpdateEndpoint applies a partial update. String fields are replaced when
// non-empty; list/object fields are replaced wholesale when non-zero.
// Deprecated is only applied when the pointer is set.
func (d *Document) UpdateEndpoint(ep *Endpoint, deprecatedSet bool) error {
	method, err := normalizeMethod(ep.Method)
	if err != nil {
		return err
	}
	path, err := normalizePath(ep.Path)
	if err != nil {
		return err
	}
	op := d.operationNode(method, path)
	if op == nil {
		return &ErrNotFound{Method: method, Path: path}
	}
	if ep.OperationID != "" {
		mapSet(op, "operationId", strNode(ep.OperationID))
	}
	if ep.Summary != "" {
		mapSet(op, "summary", strNode(ep.Summary))
	}
	if ep.Description != "" {
		mapSet(op, "description", strNode(ep.Description))
	}
	if len(ep.Tags) > 0 {
		mapSet(op, "tags", seqOfStrings(ep.Tags))
	}
	if len(ep.Parameters) > 0 {
		mapSet(op, "parameters", encodeParameters(ep.Parameters))
	}
	if ep.RequestBody != nil {
		mapSet(op, "requestBody", encodeRequestBody(ep.RequestBody))
	}
	if len(ep.Responses) > 0 {
		mapSet(op, "responses", encodeResponses(ep.Responses))
	}
	if ep.Security != nil {
		// nil = leave unchanged; non-nil (even empty) = replace. An empty
		// list encodes `security: []`, which means "explicitly public".
		mapSet(op, "security", encodeSecurity(ep.Security))
	}
	if deprecatedSet {
		if ep.Deprecated {
			mapSet(op, "deprecated", boolNode(true))
		} else {
			mapDelete(op, "deprecated")
		}
	}
	return nil
}

// DeleteEndpoint removes an operation, cleaning up empty path items.
func (d *Document) DeleteEndpoint(method, path string) error {
	m, err := normalizeMethod(method)
	if err != nil {
		return err
	}
	paths := d.PathsNode()
	item := mapGet(paths, path)
	if item == nil || mapGet(item, strings.ToLower(m)) == nil {
		return &ErrNotFound{Method: m, Path: path}
	}
	mapDelete(item, strings.ToLower(m))
	if len(mapKeys(item)) == 0 {
		mapDelete(paths, path)
	}
	return nil
}

// GenerateOperationID derives a camelCase operationId:
// get /pets/{id} -> getPetsById, post /user-profiles -> postUserProfiles.
func GenerateOperationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		isParam := strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
		seg = strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		if isParam {
			b.WriteString("By")
		}
		parts := strings.FieldsFunc(seg, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
		for _, part := range parts {
			if part == "" {
				continue
			}
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

// deriveTag guesses a tag from the first static path segment.
func deriveTag(path string) string {
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || strings.HasPrefix(seg, "{") {
			continue
		}
		return seg
	}
	return ""
}

func encodeParameters(params []Parameter) *yaml.Node {
	seq := newSequence()
	for _, p := range params {
		n := newMapping()
		mapSet(n, "name", strNode(p.Name))
		mapSet(n, "in", strNode(p.In))
		if p.Description != "" {
			mapSet(n, "description", strNode(p.Description))
		}
		if p.Required {
			mapSet(n, "required", boolNode(true))
		}
		if p.Example != "" {
			mapSet(n, "example", strNode(p.Example))
		}
		if p.Schema != nil {
			if sn, err := nodeFromAny(p.Schema); err == nil {
				mapSet(n, "schema", sn)
			}
		}
		seq.Content = append(seq.Content, n)
	}
	return seq
}

func encodeRequestBody(rb *RequestBody) *yaml.Node {
	n := newMapping()
	if rb.Description != "" {
		mapSet(n, "description", strNode(rb.Description))
	}
	if rb.Required {
		mapSet(n, "required", boolNode(true))
	}
	ct := rb.ContentType
	if ct == "" {
		ct = "application/json"
	}
	media := encodeMedia(ct, rb.SchemaRef, rb.Schema, rb.Example)
	content := newMapping()
	mapSet(content, ct, media)
	mapSet(n, "content", content)
	return n
}

// encodeSecurity renders security requirements as a YAML sequence. An empty
// (non-nil) list encodes `security: []`, meaning "explicitly public".
func encodeSecurity(reqs []map[string][]string) *yaml.Node {
	seq := newSequence()
	for _, req := range reqs {
		n := newMapping()
		for _, name := range sortedReqKeys(req) {
			mapSet(n, name, seqOfStrings(req[name]))
		}
		seq.Content = append(seq.Content, n)
	}
	return seq
}

func sortedReqKeys(req map[string][]string) []string {
	keys := make([]string, 0, len(req))
	for k := range req {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func encodeResponses(responses map[string]Response) *yaml.Node {
	n := newMapping()
	for code, r := range responses {
		rn := newMapping()
		desc := r.Description
		if desc == "" {
			desc = defaultResponseDescription(code)
		}
		mapSet(rn, "description", strNode(desc))
		if r.ContentType != "" || r.SchemaRef != "" || r.Schema != nil || r.Example != nil {
			ct := r.ContentType
			if ct == "" {
				ct = "application/json"
			}
			content := newMapping()
			mapSet(content, ct, encodeMedia(ct, r.SchemaRef, r.Schema, r.Example))
			mapSet(rn, "content", content)
		}
		mapSet(n, code, rn)
	}
	return n
}

func encodeMedia(ct, schemaRef string, schema map[string]any, example any) *yaml.Node {
	media := newMapping()
	if schemaRef != "" {
		ref := schemaRef
		if !strings.HasPrefix(ref, "#/") {
			ref = "#/components/schemas/" + ref
		}
		s := newMapping()
		mapSet(s, "$ref", strNode(ref))
		mapSet(media, "schema", s)
	} else if schema != nil {
		if sn, err := nodeFromAny(schema); err == nil {
			mapSet(media, "schema", sn)
		}
	}
	if example != nil {
		if en, err := nodeFromAny(example); err == nil {
			mapSet(media, "example", en)
		}
	}
	_ = ct
	return media
}

func defaultResponseDescription(code string) string {
	switch {
	case strings.HasPrefix(code, "2"):
		return "Successful response"
	case strings.HasPrefix(code, "4"):
		return "Client error"
	case strings.HasPrefix(code, "5"):
		return "Server error"
	}
	return "Response"
}
