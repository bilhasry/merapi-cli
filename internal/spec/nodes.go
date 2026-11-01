package spec

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file contains helpers for surgically editing a YAML node tree while
// preserving comments and formatting of untouched regions of the document.

// rootNode unwraps a document node to its root mapping.
func rootNode(doc *yaml.Node) *yaml.Node {
	if doc != nil && doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

// mapGet returns the value node for key, or nil.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapGetOrCreate returns the mapping for key, creating an empty mapping if
// missing. Empty flow mappings (e.g. `paths: {}` in a skeleton) are promoted
// to block style so added content stays readable.
func mapGetOrCreate(m *yaml.Node, key string) *yaml.Node {
	if v := mapGet(m, key); v != nil {
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			nv := newMapping()
			replaceNode(v, nv)
			return nv
		}
		if v.Kind == yaml.MappingNode && v.Style == yaml.FlowStyle && len(v.Content) == 0 {
			v.Style = 0
		}
		return v
	}
	nv := newMapping()
	mapSet(m, key, nv)
	return nv
}

// mapSet sets key to val in the mapping (replacing an existing value in place
// so its position and comments are kept).
func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	if m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, k, val)
}

// mapDelete removes key from the mapping if present.
func mapDelete(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// mapKeys returns mapping keys in document order.
func mapKeys(m *yaml.Node) []string {
	var keys []string
	if m == nil || m.Kind != yaml.MappingNode {
		return keys
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	return keys
}

// replaceNode mutates dst in place to take the kind/value/style of src.
// (Used instead of pointer replacement when the node address is referenced
// elsewhere in the tree.)
func replaceNode(dst, src *yaml.Node) {
	dst.Kind = src.Kind
	dst.Style = src.Style
	dst.Tag = src.Tag
	dst.Value = src.Value
	dst.Content = src.Content
	dst.Alias = src.Alias
}

func newMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func newSequence() *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
}

func strNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func boolNode(v bool) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool"}
	if v {
		n.Value = "true"
	} else {
		n.Value = "false"
	}
	return n
}

func intNode(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}
}

// nodeFromAny converts an arbitrary Go value to a YAML node.
func nodeFromAny(v any) (*yaml.Node, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	return rootNode(&n), nil
}

// anyFromNode decodes a YAML node into plain Go values.
func anyFromNode(n *yaml.Node) (any, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// seqOfStrings builds a YAML sequence of strings.
func seqOfStrings(items []string) *yaml.Node {
	s := newSequence()
	for _, item := range items {
		s.Content = append(s.Content, strNode(item))
	}
	return s
}

// decodeStringSlice decodes a YAML sequence node into []string.
func decodeStringSlice(n *yaml.Node) []string {
	var out []string
	if n == nil || n.Kind != yaml.SequenceNode {
		return out
	}
	for _, item := range n.Content {
		out = append(out, item.Value)
	}
	return out
}

// normalizeMethod uppercases an HTTP method.
func normalizeMethod(m string) (string, error) {
	m = strings.ToUpper(strings.TrimSpace(m))
	switch m {
	case "GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE":
		return m, nil
	}
	return "", fmt.Errorf("invalid HTTP method %q", m)
}

// normalizePath validates an OpenAPI path template.
func normalizePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p[0] != '/' {
		return "", fmt.Errorf("path must start with '/', got %q", p)
	}
	if strings.ContainsAny(p, "?# ") {
		return "", fmt.Errorf("path must not contain query strings, fragments or spaces: %q", p)
	}
	return p, nil
}
