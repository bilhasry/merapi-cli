package spec

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaSummary describes one components/schemas entry.
type SchemaSummary struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Properties  int      `json:"properties"`
	Required    []string `json:"required,omitempty"`
}

// schemasNode returns (creating if needed) the components/schemas mapping.
func (d *Document) schemasNode() *yaml.Node {
	return mapGetOrCreate(d.ComponentsNode(), "schemas")
}

// ListSchemas returns schema summaries sorted by name.
func (d *Document) ListSchemas() []SchemaSummary {
	schemas := d.schemasNode()
	var out []SchemaSummary
	for _, name := range mapKeys(schemas) {
		n := mapGet(schemas, name)
		if n == nil || n.Kind != yaml.MappingNode {
			continue
		}
		s := SchemaSummary{Name: name, Title: nodeValue(n, "title"), Description: nodeValue(n, "description")}
		if t := mapGet(n, "type"); t != nil {
			switch t.Kind {
			case yaml.ScalarNode:
				s.Type = t.Value
			case yaml.SequenceNode:
				s.Type = strings.Join(decodeStringSlice(t), " | ")
			}
		}
		if props := mapGet(n, "properties"); props != nil {
			s.Properties = len(mapKeys(props))
		}
		s.Required = decodeStringSlice(mapGet(n, "required"))
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetSchema returns the raw schema object for a name, or nil.
func (d *Document) GetSchema(name string) map[string]any {
	n := mapGet(d.schemasNode(), name)
	if n == nil {
		return nil
	}
	var m map[string]any
	if err := n.Decode(&m); err != nil {
		return nil
	}
	return m
}

// ErrSchemaExists signals the schema already exists.
type ErrSchemaExists struct{ Name string }

func (e *ErrSchemaExists) Error() string {
	return fmt.Sprintf("schema %q already exists (pass --force to overwrite)", e.Name)
}

// AddSchema inserts (or overwrites) a components/schemas entry.
func (d *Document) AddSchema(name string, schema map[string]any, force bool) error {
	if name == "" {
		return fmt.Errorf("schema name is required")
	}
	schemas := d.schemasNode()
	if mapGet(schemas, name) != nil && !force {
		return &ErrSchemaExists{Name: name}
	}
	n, err := nodeFromAny(schema)
	if err != nil {
		return fmt.Errorf("encoding schema: %w", err)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("schema for %q must be a mapping", name)
	}
	mapSet(schemas, name, n)
	return nil
}

// DeleteSchema removes a components/schemas entry, refusing when still
// referenced by an operation.
func (d *Document) DeleteSchema(name string) error {
	schemas := d.schemasNode()
	if mapGet(schemas, name) == nil {
		return fmt.Errorf("schema %q not found", name)
	}
	for _, ep := range d.ListEndpoints(EndpointFilter{}) {
		full := d.GetEndpoint(ep.Method, ep.Path)
		if full == nil {
			continue
		}
		for _, ref := range schemaRefs(full) {
			if ref == name {
				return fmt.Errorf("schema %q is still referenced by %s %s", strings.ToUpper(ep.Method), ep.Path, name)
			}
		}
	}
	mapDelete(schemas, name)
	return nil
}

// schemaRefs collects component schema names referenced by an endpoint.
func schemaRefs(ep *Endpoint) []string {
	var refs []string
	collect := func(ref string) {
		if strings.HasPrefix(ref, "#/components/schemas/") {
			refs = append(refs, strings.TrimPrefix(ref, "#/components/schemas/"))
		} else if ref != "" && !strings.Contains(ref, "/") {
			refs = append(refs, ref)
		}
	}
	if ep.RequestBody != nil {
		collect(ep.RequestBody.SchemaRef)
	}
	var codes []string
	for code := range ep.Responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		collect(ep.Responses[code].SchemaRef)
	}
	return refs
}
