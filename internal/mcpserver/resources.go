package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/bilhasry/merapi/internal/runner"
	"github.com/bilhasry/merapi/internal/spec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerResources exposes the spec and test files as MCP resources so agents
// can read project state without shelling out.
func registerResources(s *mcp.Server, root string) {
	s.AddResource(&mcp.Resource{
		URI:         "merapi://project",
		Name:        "project-overview",
		Description: "Overview of the merapi project: config, counts, active environment.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", p.Config.Name)
		fmt.Fprintf(&b, "- spec: `%s`\n- tests: `%s`\n- environments: `%s`\n- active environment: `%s`\n\n",
			p.Config.Spec, p.Config.TestsDir, p.Config.EnvironmentsDir, p.ActiveEnv())
		if doc, err := openDoc(p); err == nil {
			fmt.Fprintf(&b, "## API: %s (v%s)\n\n", doc.Title(), doc.Version())
			eps := doc.ListEndpoints(spec.EndpointFilter{})
			if len(eps) == 0 {
				b.WriteString("_no endpoints yet_\n")
			}
			for _, ep := range eps {
				fmt.Fprintf(&b, "- `%s %s` %s\n", ep.Method, ep.Path, ep.Summary)
			}
			if schemas := doc.ListSchemas(); len(schemas) > 0 {
				b.WriteString("\n## Schemas\n\n")
				for _, sc := range schemas {
					fmt.Fprintf(&b, "- `%s` (%s, %d properties)\n", sc.Name, orDash(sc.Type), sc.Properties)
				}
			}
		}
		if files, err := runner.Discover(p.TestsPath()); err == nil {
			fmt.Fprintf(&b, "\n## Tests (%d)\n\n", len(files))
			for _, f := range files {
				fmt.Fprintf(&b, "- `%s`\n", baseName(f))
			}
		}
		if names, err := envs.List(p); err == nil {
			b.WriteString("\n## Environments\n\n")
			for _, n := range names {
				marker := ""
				if n == p.ActiveEnv() {
					marker = " (active)"
				}
				fmt.Fprintf(&b, "- `%s`%s\n", n, marker)
			}
		}
		return textResource("merapi://project", "text/markdown", b.String()), nil
	})

	s.AddResource(&mcp.Resource{
		URI:         "merapi://openapi",
		Name:        "openapi-document",
		Description: "The raw OpenAPI document (YAML or JSON) managed by this project.",
		MIMEType:    "application/yaml",
	}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(p.SpecPath())
		if err != nil {
			return nil, err
		}
		mime := "application/yaml"
		if strings.HasSuffix(p.SpecPath(), ".json") {
			mime = "application/json"
		}
		return textResource("merapi://openapi", mime, string(data)), nil
	})

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "merapi://tests/{file}",
		Name:        "test-scenario",
		Description: "A single test scenario file from the tests directory, e.g. merapi://tests/get-pets.yaml.",
		MIMEType:    "application/yaml",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, err
		}
		file := strings.TrimPrefix(req.Params.URI, "merapi://tests/")
		path, err := safeTestPath(p, file)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, "application/yaml", string(data)), nil
	})
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}},
	}
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func orDash(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
