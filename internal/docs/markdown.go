package docs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bilhasry/merapi/internal/spec"
	"gopkg.in/yaml.v3"
)

// ToJSON converts a YAML/JSON spec blob to canonical JSON bytes.
func ToJSON(data []byte) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		// Maybe already JSON.
		var probe any
		if jerr := json.Unmarshal(data, &probe); jerr == nil {
			return data, nil
		}
		return nil, fmt.Errorf("parsing spec: %w", err)
	}
	return json.Marshal(v)
}

// Markdown renders a human-readable API reference from the document.
func Markdown(doc *spec.Document) []byte {
	var b strings.Builder
	title := doc.Title()
	if title == "" {
		title = "API"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	if desc := strings.TrimSpace(doc.Description()); desc != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(desc))
	}
	fmt.Fprintf(&b, "**Version:** %s\n\n", orDash(doc.Version()))
	if servers := doc.Servers(); len(servers) > 0 {
		fmt.Fprintf(&b, "**Servers:**\n")
		for _, s := range servers {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
		b.WriteString("\n")
	}

	// Endpoints grouped by first tag.
	eps := doc.ListEndpoints(spec.EndpointFilter{})
	if len(eps) == 0 {
		b.WriteString("_No endpoints documented yet. Add one with `merapi endpoint add`._\n")
	} else {
		groups := map[string][]spec.EndpointSummary{}
		var order []string
		for _, ep := range eps {
			tag := "(untagged)"
			if len(ep.Tags) > 0 {
				tag = ep.Tags[0]
			}
			if _, ok := groups[tag]; !ok {
				order = append(order, tag)
			}
			groups[tag] = append(groups[tag], ep)
		}
		sort.Strings(order)
		// Keep "(untagged)" last.
		for i, t := range order {
			if t == "(untagged)" {
				order = append(order[:i], order[i+1:]...)
				order = append(order, t)
				break
			}
		}
		b.WriteString("## Endpoints\n")
		for _, tag := range order {
			fmt.Fprintf(&b, "\n### %s\n\n", tag)
			b.WriteString("| Method | Path | Summary | Responses |\n|---|---|---|---|\n")
			for _, ep := range groups[tag] {
				responses := strings.Join(sortedCopy(ep.Responses), ", ")
				fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n",
					ep.Method, ep.Path, cell(ep.Summary), cell(responses))
			}
		}

		// Details per endpoint.
		b.WriteString("\n## Endpoint details\n")
		base := BaseURL(doc, "")
		for _, summary := range eps {
			ep := doc.GetEndpoint(summary.Method, summary.Path)
			if ep == nil {
				continue
			}
			fmt.Fprintf(&b, "\n### `%s` %s\n\n", ep.Method, ep.Path)
			if ep.OperationID != "" {
				fmt.Fprintf(&b, "`operationId: %s`\n\n", ep.OperationID)
			}
			if ep.Summary != "" {
				fmt.Fprintf(&b, "%s\n\n", ep.Summary)
			}
			if desc := strings.TrimSpace(ep.Description); desc != "" {
				fmt.Fprintf(&b, "%s\n\n", desc)
			}
			if len(ep.Tags) > 0 {
				fmt.Fprintf(&b, "**Tags:** %s\n\n", strings.Join(ep.Tags, ", "))
			}
			if len(ep.Parameters) > 0 {
				b.WriteString("**Parameters**\n\n| Name | In | Type | Required | Description |\n|---|---|---|---|---|\n")
				for _, p := range ep.Parameters {
					typ := "string"
					if p.Schema != nil {
						if t, ok := p.Schema["type"].(string); ok {
							typ = t
						}
					}
					fmt.Fprintf(&b, "| `%s` | %s | %s | %v | %s |\n",
						p.Name, p.In, typ, p.Required, cell(p.Description))
				}
				b.WriteString("\n")
			}
			if ep.RequestBody != nil {
				ct := ep.RequestBody.ContentType
				if ct == "" {
					ct = "application/json"
				}
				fmt.Fprintf(&b, "**Request body** (`%s`, required=%v)\n\n", ct, ep.RequestBody.Required)
				if ep.RequestBody.SchemaRef != "" {
					fmt.Fprintf(&b, "- Schema: `%s`\n", refName(ep.RequestBody.SchemaRef))
				}
				if ep.RequestBody.Description != "" {
					fmt.Fprintf(&b, "- %s\n", ep.RequestBody.Description)
				}
				b.WriteString("\n")
			}
			if len(ep.Responses) > 0 {
				codes := sortedCopy(keysOf(ep.Responses))
				b.WriteString("**Responses**\n\n| Status | Description | Content type | Schema |\n|---|---|---|---|\n")
				for _, code := range codes {
					r := ep.Responses[code]
					schema := refName(r.SchemaRef)
					if schema == "" && r.Schema != nil {
						if t, ok := r.Schema["type"].(string); ok {
							schema = t
						}
					}
					fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", code, cell(r.Description), cell(r.ContentType), cell(schema))
				}
				b.WriteString("\n")
			}
			if samples, err := Samples(doc, ep, base, nil); err == nil && len(samples) > 0 {
				b.WriteString("**Request examples**\n\n<details>\n<summary>Code samples (cURL, Go, Python, JavaScript)</summary>\n\n")
				for _, s := range samples {
					fmt.Fprintf(&b, "#### %s\n\n```%s\n%s```\n\n", SampleDisplayName(s.Language), SampleFence(s.Language), s.Code)
				}
				b.WriteString("</details>\n\n")
			}
		}
	}

	// Schemas.
	if schemas := doc.ListSchemas(); len(schemas) > 0 {
		b.WriteString("## Schemas\n")
		for _, s := range schemas {
			fmt.Fprintf(&b, "\n### `%s`\n\n", s.Name)
			if s.Description != "" {
				fmt.Fprintf(&b, "%s\n\n", s.Description)
			}
			if s.Type != "" {
				fmt.Fprintf(&b, "- Type: `%s`\n", s.Type)
			}
			if s.Properties > 0 {
				fmt.Fprintf(&b, "- Properties: %d\n", s.Properties)
			}
			if len(s.Required) > 0 {
				fmt.Fprintf(&b, "- Required: `%s`\n", strings.Join(s.Required, "`, `"))
			}
			full := doc.GetSchema(s.Name)
			if full != nil {
				if props, ok := full["properties"].(map[string]any); ok && len(props) > 0 {
					b.WriteString("\n| Property | Type | Description |\n|---|---|---|\n")
					names := keysOfAny(props)
					for _, name := range names {
						p := props[name]
						typ := ""
						desc := ""
						if pm, ok := p.(map[string]any); ok {
							if t, ok := pm["type"].(string); ok {
								typ = t
							}
							if r, ok := pm["$ref"].(string); ok {
								typ = refName(r)
							}
							desc, _ = pm["description"].(string)
						}
						fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, cell(typ), cell(desc))
					}
				}
			}
			b.WriteString("\n")
		}
	}

	fmt.Fprintf(&b, "\n---\n\n_Generated by merapi._\n")
	return []byte(b.String())
}

func refName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "—"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysOfAny(m map[string]any) []string {
	return keysOf(m)
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}
