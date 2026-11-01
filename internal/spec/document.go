package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Document is an OpenAPI 3.1 document backed by a YAML node tree.
// Edits are applied surgically so comments in untouched regions survive.
type Document struct {
	Path string
	root *yaml.Node
}

// NewSkeleton creates a minimal valid OpenAPI 3.1 document.
func NewSkeleton(path, title, version, description, serverURL string) *Document {
	if version == "" {
		version = "0.1.0"
	}
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}
	if description == "" {
		description = "API documentation managed by merapi."
	}
	skeleton := fmt.Sprintf(`openapi: 3.1.0
info:
  title: %s
  version: %s
  description: |
    %s
servers:
  - url: %s
    description: Default server
paths: {}
components:
  schemas: {}
`, title, version, description, serverURL)
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(skeleton), &doc); err != nil {
		panic(err)
	}
	return &Document{Path: path, root: rootNode(&doc)}
}

// Load reads an OpenAPI document from disk.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	root := rootNode(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s does not contain an OpenAPI document", path)
	}
	return &Document{Path: path, root: root}, nil
}

// Save writes the document back to disk.
func (d *Document) Save() error {
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.root); err != nil {
		return fmt.Errorf("rendering document: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("rendering document: %w", err)
	}
	return os.WriteFile(d.Path, buf.Bytes(), 0o644)
}

// Root returns the root mapping node of the document.
func (d *Document) Root() *yaml.Node { return d.root }

// Title returns info.title.
func (d *Document) Title() string {
	if n := mapGet(mapGet(d.root, "info"), "title"); n != nil {
		return n.Value
	}
	return ""
}

// Version returns info.version.
func (d *Document) Version() string {
	if n := mapGet(mapGet(d.root, "info"), "version"); n != nil {
		return n.Value
	}
	return ""
}

// Description returns info.description.
func (d *Document) Description() string {
	if n := mapGet(mapGet(d.root, "info"), "description"); n != nil {
		return n.Value
	}
	return ""
}

// SetInfo updates info.title/version/description. Empty values are skipped.
func (d *Document) SetInfo(title, version, description string) {
	info := mapGetOrCreate(d.root, "info")
	if title != "" {
		mapSet(info, "title", strNode(title))
	}
	if version != "" {
		mapSet(info, "version", strNode(version))
	}
	if description != "" {
		mapSet(info, "description", strNode(description))
	}
}

// Servers returns the declared server URLs.
func (d *Document) Servers() []string {
	var out []string
	servers := mapGet(d.root, "servers")
	if servers == nil || servers.Kind != yaml.SequenceNode {
		return out
	}
	for _, s := range servers.Content {
		if u := mapGet(s, "url"); u != nil {
			out = append(out, u.Value)
		}
	}
	return out
}

// SetServer sets the first server URL (creating the entry when needed).
func (d *Document) SetServer(url string) {
	servers := mapGet(d.root, "servers")
	if servers == nil || servers.Kind != yaml.SequenceNode || len(servers.Content) == 0 {
		servers = newSequence()
		servers.Content = append(servers.Content, newMapping())
		mapSet(d.root, "servers", servers)
	}
	first := servers.Content[0]
	if first.Kind != yaml.MappingNode {
		first = newMapping()
		servers.Content[0] = first
	}
	mapSet(first, "url", strNode(url))
}

// PathsNode returns (creating if needed) the paths mapping.
func (d *Document) PathsNode() *yaml.Node {
	return mapGetOrCreate(d.root, "paths")
}

// ComponentsNode returns (creating if needed) the components mapping.
func (d *Document) ComponentsNode() *yaml.Node {
	return mapGetOrCreate(d.root, "components")
}

// JSON renders the document as JSON bytes (used by doc preview and validation).
func (d *Document) JSON() ([]byte, error) {
	var v any
	if err := d.root.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
