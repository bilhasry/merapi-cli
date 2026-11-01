package spec

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// SecurityScheme mirrors an OpenAPI securityScheme object (subset).
//
// Note on naming: OpenAPI uses `name` for the scheme's map key AND (for
// apiKey schemes) for the credential parameter's name. Go's yaml/json tags
// cannot express both, so Name carries the scheme key and ParameterName the
// apiKey parameter (`name` in YAML).
type SecurityScheme struct {
	Name             string         `json:"name" yaml:"name"` // scheme key under securitySchemes
	Type             string         `json:"type" yaml:"type"` // apiKey, http, oauth2, openIdConnect, mutualTLS
	Description      string         `json:"description,omitempty" yaml:"description,omitempty"`
	In               string         `json:"in,omitempty" yaml:"in,omitempty"`                       // apiKey: header, query, cookie
	ParameterName    string         `json:"parameterName,omitempty" yaml:"parameterName,omitempty"` // apiKey: credential parameter name
	Scheme           string         `json:"scheme,omitempty" yaml:"scheme,omitempty"`               // http: bearer, basic, digest...
	BearerFormat     string         `json:"bearerFormat,omitempty" yaml:"bearerFormat,omitempty"`   // e.g. JWT
	OpenIdConnectUrl string         `json:"openIdConnectUrl,omitempty" yaml:"openIdConnectUrl,omitempty"`
	Flows            map[string]any `json:"flows,omitempty" yaml:"flows,omitempty"` // oauth2, kept opaque
}

// Describe renders a short human-readable summary ("apiKey in header X-API-Key").
func (s SecurityScheme) Describe() string {
	switch s.Type {
	case "apiKey":
		return fmt.Sprintf("apiKey in %s %s", s.In, s.ParameterName)
	case "http":
		d := "http " + s.Scheme
		if s.BearerFormat != "" {
			d += " (" + s.BearerFormat + ")"
		}
		return d
	case "oauth2":
		return "oauth2"
	case "openIdConnect":
		return "openIdConnect"
	case "mutualTLS":
		return "mutualTLS"
	}
	return s.Type
}

// decodeSecurityNode turns a YAML `security:` sequence into requirement maps.
// An empty sequence decodes to an empty non-nil slice ("explicitly public"),
// distinct from an absent key (nil, "inherit document-level").
func decodeSecurityNode(n *yaml.Node) []map[string][]string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := []map[string][]string{}
	for _, req := range n.Content {
		if req.Kind != yaml.MappingNode {
			continue
		}
		m := map[string][]string{}
		for _, k := range mapKeys(req) {
			m[k] = decodeStringSlice(mapGet(req, k))
		}
		out = append(out, m)
	}
	return out
}

// securitySchemesNode returns (creating if needed) components/securitySchemes.
func (d *Document) securitySchemesNode() *yaml.Node {
	return mapGetOrCreate(d.ComponentsNode(), "securitySchemes")
}

// ListSecuritySchemes returns all schemes in document order.
func (d *Document) ListSecuritySchemes() []SecurityScheme {
	node := d.securitySchemesNode()
	var out []SecurityScheme
	for _, name := range mapKeys(node) {
		if s := decodeScheme(name, mapGet(node, name)); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// GetSecurityScheme returns one scheme by name, or nil.
func (d *Document) GetSecurityScheme(name string) *SecurityScheme {
	return decodeScheme(name, mapGet(d.securitySchemesNode(), name))
}

func decodeScheme(name string, n *yaml.Node) *SecurityScheme {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	s := &SecurityScheme{
		Name:             name,
		Type:             nodeValue(n, "type"),
		Description:      nodeValue(n, "description"),
		In:               nodeValue(n, "in"),
		ParameterName:    nodeValue(n, "name"),
		Scheme:           nodeValue(n, "scheme"),
		BearerFormat:     nodeValue(n, "bearerFormat"),
		OpenIdConnectUrl: nodeValue(n, "openIdConnectUrl"),
	}
	if flows := mapGet(n, "flows"); flows != nil {
		if m, err := anyFromNode(flows); err == nil {
			if fm, ok := m.(map[string]any); ok {
				s.Flows = fm
			}
		}
	}
	return s
}

// AddSecurityScheme inserts or replaces a components/securitySchemes entry.
// The scheme is validated: apiKey needs name+in, http needs a scheme.
func (d *Document) AddSecurityScheme(s SecurityScheme) error {
	if s.Name == "" {
		return fmt.Errorf("scheme name is required")
	}
	switch s.Type {
	case "apiKey":
		switch s.In {
		case "header", "query", "cookie":
		case "":
			return fmt.Errorf("apiKey scheme %q requires --in header|query|cookie", s.Name)
		default:
			return fmt.Errorf("apiKey scheme %q: --in must be header, query or cookie (got %q)", s.Name, s.In)
		}
		if s.ParameterName == "" {
			return fmt.Errorf("apiKey scheme %q requires a credential parameter name (--name)", s.Name)
		}
	case "http":
		if s.Scheme == "" {
			return fmt.Errorf("http scheme %q requires --scheme (bearer, basic, ...)", s.Name)
		}
	case "oauth2":
		if s.Flows == nil {
			// A scheme without flows is unusual but valid; keep it simple.
			s.Flows = map[string]any{}
		}
	case "openIdConnect":
		if s.OpenIdConnectUrl == "" {
			return fmt.Errorf("openIdConnect scheme %q requires an openIdConnectUrl", s.Name)
		}
	case "mutualTLS":
	case "":
		return fmt.Errorf("scheme %q requires a type (apiKey, http, oauth2, openIdConnect, mutualTLS)", s.Name)
	default:
		return fmt.Errorf("unknown scheme type %q for %q (want apiKey, http, oauth2, openIdConnect or mutualTLS)", s.Type, s.Name)
	}

	n := newMapping()
	mapSet(n, "type", strNode(s.Type))
	if s.Description != "" {
		mapSet(n, "description", strNode(s.Description))
	}
	switch s.Type {
	case "apiKey":
		mapSet(n, "in", strNode(s.In))
		mapSet(n, "name", strNode(s.ParameterName))
	case "http":
		mapSet(n, "scheme", strNode(s.Scheme))
		if s.BearerFormat != "" {
			mapSet(n, "bearerFormat", strNode(s.BearerFormat))
		}
	case "oauth2":
		if fn, err := nodeFromAny(s.Flows); err == nil {
			mapSet(n, "flows", fn)
		}
	case "openIdConnect":
		mapSet(n, "openIdConnectUrl", strNode(s.OpenIdConnectUrl))
	}
	mapSet(d.securitySchemesNode(), s.Name, n)
	return nil
}

// DeleteSecurityScheme removes a scheme, refusing while an operation still
// references it.
func (d *Document) DeleteSecurityScheme(name string) error {
	node := d.securitySchemesNode()
	if mapGet(node, name) == nil {
		return fmt.Errorf("security scheme %q not found", name)
	}
	for _, ep := range d.ListEndpoints(EndpointFilter{}) {
		full := d.GetEndpoint(ep.Method, ep.Path)
		if full == nil {
			continue
		}
		for _, req := range full.Security {
			if _, used := req[name]; used {
				return fmt.Errorf("scheme %q is still used by %s %s", name, ep.Method, ep.Path)
			}
		}
	}
	if _, used := d.documentSecurity()[name]; used {
		return fmt.Errorf("scheme %q is still used by the document-level security", name)
	}
	mapDelete(node, name)
	return nil
}

// Security returns the document-level security requirements (applied to every
// operation without its own `security`).
func (d *Document) Security() []map[string][]string {
	return decodeSecurityNode(mapGet(d.root, "security"))
}

// SetSecurity replaces the document-level security requirements. Pass an
// empty (non-nil) list to clear it, nil to remove the key entirely.
func (d *Document) SetSecurity(reqs []map[string][]string) {
	if reqs == nil {
		mapDelete(d.root, "security")
		return
	}
	mapSet(d.root, "security", encodeSecurity(reqs))
}

// documentSecurity decodes the document-level security requirements into a
// flat scheme-name map (for reference checks).
func (d *Document) documentSecurity() map[string][]string {
	out := map[string][]string{}
	for _, req := range d.Security() {
		for name, scopes := range req {
			out[name] = scopes
		}
	}
	return out
}

// SecuritySchemesUsing lists schemes referenced by operations (and the
// document-level security), keyed by scheme name.
func (d *Document) SecuritySchemesUsing() map[string][]string {
	out := map[string][]string{}
	for name := range d.documentSecurity() {
		out[name] = append(out[name], "(document)")
	}
	for _, ep := range d.ListEndpoints(EndpointFilter{}) {
		full := d.GetEndpoint(ep.Method, ep.Path)
		if full == nil {
			continue
		}
		for _, req := range full.Security {
			for name := range req {
				key := opKey(ep.Method, ep.Path)
				if !containsString(out[name], key) {
					out[name] = append(out[name], key)
				}
			}
		}
	}
	for name := range out {
		sort.Strings(out[name])
	}
	return out
}

// UndefinedSecuritySchemes returns operations whose security references a
// scheme not defined in components/securitySchemes.
func (d *Document) UndefinedSecuritySchemes() []string {
	defined := map[string]bool{}
	for _, s := range d.ListSecuritySchemes() {
		defined[s.Name] = true
	}
	var out []string
	for _, ep := range d.ListEndpoints(EndpointFilter{}) {
		full := d.GetEndpoint(ep.Method, ep.Path)
		if full == nil {
			continue
		}
		for _, req := range full.Security {
			for name := range req {
				if !defined[name] {
					out = append(out, fmt.Sprintf("%s %s references undefined security scheme %q",
						ep.Method, ep.Path, name))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
