// Package mcpserver exposes merapi as a Model Context Protocol server so any
// MCP-capable AI agent (Claude, Cursor, custom agents, ...) can manage API
// documentation, tests and environments through typed tools.
//
// Start it with:  merapi mcp
package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bilhasry/merapi/internal/docs"
	"github.com/bilhasry/merapi/internal/envs"
	"github.com/bilhasry/merapi/internal/gql"
	"github.com/bilhasry/merapi/internal/project"
	"github.com/bilhasry/merapi/internal/runner"
	"github.com/bilhasry/merapi/internal/spec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// lock serializes mutations so concurrent agent sessions cannot corrupt files.
var lock sync.Mutex

const instructions = `merapi manages an API documentation project: an OpenAPI 3.1 document,
YAML test scenarios, and environments with variables and encrypted secrets.

Recommended workflow for documenting an API:
1. Call spec_summary to see what exists, then spec_validate to check quality.
2. Add schemas with schema_add (components/schemas) before referencing them.
3. Add operations with endpoint_add (method, path, summary, description, tags,
   requestBody with schemaRef, responses with status codes).
4. Call spec_validate again; fix reported errors and warnings.
5. Call test_scaffold to generate starter tests, then test_run against an
   environment to verify the documented behavior.

Conventions:
- Paths use OpenAPI templates: /pets/{id}. Requests use {{variable}} templates.
- {{base_url}}, {{name}} (variable/capture) and {{secret:NAME}} are supported
  in test requests. Secrets are read from the encrypted store and never
  returned by any tool.
- Every mutation is persisted immediately; the YAML file keeps your comments.
- tool errors are returned as messages; read them and adapt.`

// Run starts the MCP server on stdio.
func Run(ctx context.Context, root, version string) error {
	return newServer(root, version).Run(ctx, &mcp.StdioTransport{})
}

// newServer builds the fully-populated MCP server (also used by tests with an
// in-memory transport).
func newServer(root, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "merapi",
		Version: version,
	}, &mcp.ServerOptions{Instructions: instructions})

	registerTools(server, root, version)
	registerResources(server, root)
	return server
}

// openProject resolves the project rooted at root.
func openProject(root string) (*project.Project, error) {
	return project.Find(root)
}

func openDoc(p *project.Project) (*spec.Document, error) {
	return spec.Load(p.SpecPath())
}

// ---------------------------------------------------------------------------
// spec tools
// ---------------------------------------------------------------------------

type SummaryOut struct {
	Title             string   `json:"title"`
	Version           string   `json:"version,omitempty"`
	Description       string   `json:"description,omitempty"`
	Servers           []string `json:"servers,omitempty"`
	EndpointCount     int      `json:"endpointCount"`
	SchemaCount       int      `json:"schemaCount"`
	TestCount         int      `json:"testCount"`
	ActiveEnvironment string   `json:"activeEnvironment"`
	Environments      []string `json:"environments"`
	SpecFile          string   `json:"specFile"`
}

// DeleteIn identifies an endpoint to delete.
type DeleteIn struct {
	Method string `json:"method" jsonschema:"HTTP method"`
	Path   string `json:"path" jsonschema:"OpenAPI path template"`
}

// DeleteOut is the generic success payload for destructive tools.
type DeleteOut struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func registerTools(s *mcp.Server, root, version string) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "spec_summary",
		Description: "Summarize the API project: title, endpoint/schema/test counts, environments.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SummaryOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, SummaryOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, SummaryOut{}, err
		}
		files, _ := runner.Discover(p.TestsPath())
		envNames, _ := envs.List(p)
		return nil, SummaryOut{
			Title:             doc.Title(),
			Version:           doc.Version(),
			Description:       trim(doc.Description(), 400),
			Servers:           doc.Servers(),
			EndpointCount:     len(doc.ListEndpoints(spec.EndpointFilter{})),
			SchemaCount:       len(doc.ListSchemas()),
			TestCount:         len(files),
			ActiveEnvironment: p.ActiveEnv(),
			Environments:      envNames,
			SpecFile:          filepath.Base(p.SpecPath()),
		}, nil
	})

	// --- validate ---
	type ValidateOut struct {
		Valid    bool         `json:"valid"`
		Errors   []spec.Issue `json:"errors,omitempty"`
		Warnings []spec.Issue `json:"warnings,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "spec_validate",
		Description: "Validate the OpenAPI document (spec conformance errors + documentation quality warnings).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ValidateOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, ValidateOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, ValidateOut{}, err
		}
		issues, err := doc.Validate()
		if err != nil {
			return nil, ValidateOut{}, err
		}
		out := ValidateOut{Valid: true}
		for _, issue := range issues {
			if issue.Severity == "error" {
				out.Valid = false
				out.Errors = append(out.Errors, issue)
			} else {
				out.Warnings = append(out.Warnings, issue)
			}
		}
		return nil, out, nil
	})

	// --- endpoint list/get/add/update/delete ---
	type EndpointListIn struct {
		PathPrefix string `json:"pathPrefix,omitempty" jsonschema:"filter by path prefix, e.g. /pets"`
		Tag        string `json:"tag,omitempty" jsonschema:"filter by tag"`
	}
	type EndpointListOut struct {
		Endpoints []spec.EndpointSummary `json:"endpoints"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "endpoint_list",
		Description: "List documented endpoints (method, path, operationId, summary, tags, response codes).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EndpointListIn) (*mcp.CallToolResult, EndpointListOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, EndpointListOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, EndpointListOut{}, err
		}
		eps := doc.ListEndpoints(spec.EndpointFilter{PathPrefix: in.PathPrefix, Tag: in.Tag})
		if eps == nil {
			eps = []spec.EndpointSummary{}
		}
		return nil, EndpointListOut{Endpoints: eps}, nil
	})

	type EndpointGetIn struct {
		Method string `json:"method" jsonschema:"HTTP method, e.g. GET"`
		Path   string `json:"path" jsonschema:"OpenAPI path template, e.g. /pets/{id}"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "endpoint_get",
		Description: "Get the full documentation of one endpoint.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EndpointGetIn) (*mcp.CallToolResult, spec.Endpoint, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, spec.Endpoint{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, spec.Endpoint{}, err
		}
		ep := doc.GetEndpoint(in.Method, in.Path)
		if ep == nil {
			return nil, spec.Endpoint{}, fmt.Errorf("%s %s not found", strings.ToUpper(in.Method), in.Path)
		}
		return nil, *ep, nil
	})

	type EndpointAddIn struct {
		Method      string                   `json:"method" jsonschema:"HTTP method (GET, POST, ...)"`
		Path        string                   `json:"path" jsonschema:"OpenAPI path template, e.g. /pets/{id}"`
		OperationID string                   `json:"operationId,omitempty" jsonschema:"camelCase operation id; derived from method+path when omitted"`
		Summary     string                   `json:"summary,omitempty" jsonschema:"short one-line summary"`
		Description string                   `json:"description,omitempty" jsonschema:"detailed description of the operation"`
		Tags        []string                 `json:"tags,omitempty" jsonschema:"tags for grouping; derived from first path segment when omitted"`
		Parameters  []spec.Parameter         `json:"parameters,omitempty" jsonschema:"path/query/header parameters"`
		RequestBody *spec.RequestBody        `json:"requestBody,omitempty" jsonschema:"request body with contentType, schemaRef (e.g. Pet) or inline schema, and example"`
		Responses   map[string]spec.Response `json:"responses,omitempty" jsonschema:"map of status code -> response with description, contentType, schemaRef; defaults to 200"`
		Security    []map[string][]string    `json:"security,omitempty" jsonschema:"auth requirements: list of {schemeName: [scopes]} (OR between items); empty list = explicitly public"`
	}
	type EndpointAddOut struct {
		OK          bool   `json:"ok"`
		OperationID string `json:"operationId"`
		Message     string `json:"message"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "endpoint_add",
		Description: "Document a new endpoint operation in the OpenAPI document. Validates method/path, fills defaults, persists immediately.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EndpointAddIn) (*mcp.CallToolResult, EndpointAddOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, EndpointAddOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, EndpointAddOut{}, err
		}
		ep := &spec.Endpoint{
			Method:      in.Method,
			Path:        in.Path,
			OperationID: in.OperationID,
			Summary:     in.Summary,
			Description: in.Description,
			Tags:        in.Tags,
			Parameters:  in.Parameters,
			RequestBody: in.RequestBody,
			Responses:   in.Responses,
			Security:    in.Security,
		}
		if err := doc.AddEndpoint(ep); err != nil {
			return nil, EndpointAddOut{}, err
		}
		if err := doc.Save(); err != nil {
			return nil, EndpointAddOut{}, err
		}
		return nil, EndpointAddOut{
			OK:          true,
			OperationID: ep.OperationID,
			Message:     fmt.Sprintf("documented %s %s", ep.Method, ep.Path),
		}, nil
	})

	type EndpointUpdateIn struct {
		Method      string                   `json:"method" jsonschema:"HTTP method of the endpoint to update"`
		Path        string                   `json:"path" jsonschema:"path of the endpoint to update"`
		OperationID string                   `json:"operationId,omitempty"`
		Summary     string                   `json:"summary,omitempty"`
		Description string                   `json:"description,omitempty"`
		Tags        []string                 `json:"tags,omitempty" jsonschema:"replaces the full tag list"`
		Parameters  []spec.Parameter         `json:"parameters,omitempty" jsonschema:"replaces the full parameter list"`
		RequestBody *spec.RequestBody        `json:"requestBody,omitempty" jsonschema:"replaces the request body documentation"`
		Responses   map[string]spec.Response `json:"responses,omitempty" jsonschema:"replaces the full response map"`
		Deprecated  *bool                    `json:"deprecated,omitempty" jsonschema:"mark deprecated (true/false)"`
		Security    []map[string][]string    `json:"security,omitempty" jsonschema:"replaces the auth requirements list; empty list = explicitly public"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "endpoint_update",
		Description: "Update parts of an existing endpoint. Only provided fields are changed; list/object fields are replaced wholesale.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EndpointUpdateIn) (*mcp.CallToolResult, EndpointAddOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, EndpointAddOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, EndpointAddOut{}, err
		}
		ep := &spec.Endpoint{
			Method:      in.Method,
			Path:        in.Path,
			OperationID: in.OperationID,
			Summary:     in.Summary,
			Description: in.Description,
			Tags:        in.Tags,
			Parameters:  in.Parameters,
			RequestBody: in.RequestBody,
			Responses:   in.Responses,
			Security:    in.Security,
		}
		deprecatedSet := in.Deprecated != nil
		if deprecatedSet {
			ep.Deprecated = *in.Deprecated
		}
		if err := doc.UpdateEndpoint(ep, deprecatedSet); err != nil {
			return nil, EndpointAddOut{}, err
		}
		if err := doc.Save(); err != nil {
			return nil, EndpointAddOut{}, err
		}
		return nil, EndpointAddOut{
			OK:          true,
			OperationID: in.OperationID,
			Message:     fmt.Sprintf("updated %s %s", strings.ToUpper(in.Method), in.Path),
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "endpoint_delete",
		Description: "Remove an endpoint operation from the document.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteIn) (*mcp.CallToolResult, DeleteOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.DeleteEndpoint(in.Method, in.Path); err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.Save(); err != nil {
			return nil, DeleteOut{}, err
		}
		return nil, DeleteOut{OK: true, Message: fmt.Sprintf("deleted %s %s", strings.ToUpper(in.Method), in.Path)}, nil
	})

	// --- code samples ---
	type DocSamplesIn struct {
		Method    string   `json:"method" jsonschema:"HTTP method, e.g. GET"`
		Path      string   `json:"path" jsonschema:"OpenAPI path template, e.g. /pets/{id}"`
		Languages []string `json:"languages,omitempty" jsonschema:"limit to language slugs (curl, go, python, javascript); default: all"`
	}
	type DocSamplesOut struct {
		BaseURL string        `json:"baseUrl"`
		Samples []docs.Sample `json:"samples"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "doc_samples",
		Description: "Generate ready-to-paste request samples (cURL, Go, Python, JavaScript) for one endpoint, using the active environment's base URL.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DocSamplesIn) (*mcp.CallToolResult, DocSamplesOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, DocSamplesOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, DocSamplesOut{}, err
		}
		ep := doc.GetEndpoint(in.Method, in.Path)
		if ep == nil {
			return nil, DocSamplesOut{}, fmt.Errorf("%s %s not found", strings.ToUpper(in.Method), in.Path)
		}
		baseURL := ""
		if env, err := envs.Load(p, envs.Resolve(p, "")); err == nil {
			baseURL = env.BaseURL
		}
		samples, err := docs.Samples(doc, ep, baseURL, in.Languages)
		if err != nil {
			return nil, DocSamplesOut{}, err
		}
		if samples == nil {
			samples = []docs.Sample{}
		}
		return nil, DocSamplesOut{BaseURL: docs.BaseURL(doc, baseURL), Samples: samples}, nil
	})

	// --- schema tools ---
	mcp.AddTool(s, &mcp.Tool{
		Name:        "schema_list",
		Description: "List components/schemas with type, property count and required fields.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
		Schemas []spec.SchemaSummary `json:"schemas"`
	}, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, struct {
				Schemas []spec.SchemaSummary `json:"schemas"`
			}{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, struct {
				Schemas []spec.SchemaSummary `json:"schemas"`
			}{}, err
		}
		list := doc.ListSchemas()
		if list == nil {
			list = []spec.SchemaSummary{}
		}
		return nil, struct {
			Schemas []spec.SchemaSummary `json:"schemas"`
		}{Schemas: list}, nil
	})

	type SchemaGetIn struct {
		Name string `json:"name" jsonschema:"schema name, e.g. Pet"`
	}
	type SchemaOut struct {
		OK     bool           `json:"ok"`
		Name   string         `json:"name"`
		Schema map[string]any `json:"schema,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "schema_get",
		Description: "Get one components/schemas entry as a JSON Schema object.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SchemaGetIn) (*mcp.CallToolResult, SchemaOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, SchemaOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, SchemaOut{}, err
		}
		sc := doc.GetSchema(in.Name)
		if sc == nil {
			return nil, SchemaOut{OK: false, Name: in.Name}, fmt.Errorf("schema %q not found", in.Name)
		}
		return nil, SchemaOut{OK: true, Name: in.Name, Schema: sc}, nil
	})

	type SchemaAddIn struct {
		Name      string         `json:"name" jsonschema:"schema name, PascalCase, e.g. Pet"`
		Schema    map[string]any `json:"schema" jsonschema:"JSON Schema object (type, properties, required, ...)"`
		Overwrite bool           `json:"overwrite,omitempty" jsonschema:"replace an existing schema with the same name"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "schema_add",
		Description: "Add or replace a components/schemas entry. Reference it from operations via schemaRef.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SchemaAddIn) (*mcp.CallToolResult, DeleteOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.AddSchema(in.Name, in.Schema, in.Overwrite); err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.Save(); err != nil {
			return nil, DeleteOut{}, err
		}
		return nil, DeleteOut{OK: true, Message: fmt.Sprintf("schema %s saved", in.Name)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "schema_delete",
		Description: "Delete a schema (refuses if an operation still references it).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SchemaGetIn) (*mcp.CallToolResult, DeleteOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.DeleteSchema(in.Name); err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.Save(); err != nil {
			return nil, DeleteOut{}, err
		}
		return nil, DeleteOut{OK: true, Message: fmt.Sprintf("schema %s deleted", in.Name)}, nil
	})

	// --- security tools ---
	type SecurityNameIn struct {
		Name string `json:"name" jsonschema:"security scheme name, e.g. BearerAuth"`
	}
	type SecurityListOut struct {
		Schemes []spec.SecurityScheme `json:"schemes"`
		UsedBy  map[string][]string   `json:"usedBy"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "security_list",
		Description: "List components/securitySchemes (apiKey, http, oauth2, openIdConnect, mutualTLS) and which operations use each.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SecurityListOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, SecurityListOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, SecurityListOut{}, err
		}
		schemes := doc.ListSecuritySchemes()
		if schemes == nil {
			schemes = []spec.SecurityScheme{}
		}
		return nil, SecurityListOut{Schemes: schemes, UsedBy: doc.SecuritySchemesUsing()}, nil
	})

	type SecurityAddIn struct {
		Name             string         `json:"name" jsonschema:"scheme name, e.g. BearerAuth, ApiKeyAuth"`
		Type             string         `json:"type" jsonschema:"apiKey, http, oauth2, openIdConnect or mutualTLS"`
		In               string         `json:"in,omitempty" jsonschema:"apiKey only: header, query or cookie"`
		ParameterName    string         `json:"parameterName,omitempty" jsonschema:"apiKey only: credential parameter name, e.g. X-API-Key"`
		Scheme           string         `json:"scheme,omitempty" jsonschema:"http only: bearer, basic, digest..."`
		BearerFormat     string         `json:"bearerFormat,omitempty" jsonschema:"http only: token format hint, e.g. JWT"`
		Description      string         `json:"description,omitempty"`
		OpenIdConnectUrl string         `json:"openIdConnectUrl,omitempty" jsonschema:"openIdConnect only: discovery URL"`
		Flows            map[string]any `json:"flows,omitempty" jsonschema:"oauth2 only: flows object; kept opaque, never validated"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "security_add",
		Description: "Add or replace a components/securitySchemes entry. Operations then require it via the security field of endpoint_add/endpoint_update.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SecurityAddIn) (*mcp.CallToolResult, DeleteOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		scheme := spec.SecurityScheme{
			Name:             in.Name,
			Type:             in.Type,
			In:               in.In,
			ParameterName:    in.ParameterName,
			Scheme:           in.Scheme,
			BearerFormat:     in.BearerFormat,
			Description:      in.Description,
			OpenIdConnectUrl: in.OpenIdConnectUrl,
			Flows:            in.Flows,
		}
		if err := doc.AddSecurityScheme(scheme); err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.Save(); err != nil {
			return nil, DeleteOut{}, err
		}
		return nil, DeleteOut{OK: true, Message: fmt.Sprintf("security scheme %s saved (%s)", scheme.Name, scheme.Describe())}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "security_delete",
		Description: "Delete a security scheme (refuses while operations or document-level security still reference it).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SecurityNameIn) (*mcp.CallToolResult, DeleteOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.DeleteSecurityScheme(in.Name); err != nil {
			return nil, DeleteOut{}, err
		}
		if err := doc.Save(); err != nil {
			return nil, DeleteOut{}, err
		}
		return nil, DeleteOut{OK: true, Message: fmt.Sprintf("security scheme %s deleted", in.Name)}, nil
	})

	registerTestTools(s, root)
	registerEnvTools(s, root)
	registerGraphQLTools(s, root)
}

// ---------------------------------------------------------------------------
// GraphQL tools
// ---------------------------------------------------------------------------

// openGQL resolves the project's SDL file, returning a helpful error when
// none is configured.
func openGQL(root string) (*project.Project, *gql.Schema, error) {
	p, err := openProject(root)
	if err != nil {
		return nil, nil, err
	}
	path := p.GraphQLPath()
	if path == "" {
		return nil, nil, fmt.Errorf("no GraphQL schema configured — add %s at the project root, or set `graphql: <file>` in merapi.yaml",
			project.DefaultGraphQLFile)
	}
	s, err := gql.Load(path)
	if err != nil {
		return nil, nil, err
	}
	return p, s, nil
}

func registerGraphQLTools(s *mcp.Server, root string) {
	// --- gql_validate ---
	type GQLValidateOut struct {
		OK            bool   `json:"ok"`
		File          string `json:"file"`
		Types         int    `json:"types"`
		Queries       int    `json:"queries"`
		Mutations     int    `json:"mutations"`
		Subscriptions int    `json:"subscriptions"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "gql_validate",
		Description: "Validate the GraphQL SDL schema file. Returns false with details on parse/validation errors.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, GQLValidateOut, error) {
		p, sch, err := openGQL(root)
		if err != nil {
			return nil, GQLValidateOut{}, err
		}
		sum := sch.Summarize()
		return nil, GQLValidateOut{
			OK:            true,
			File:          filepath.Base(p.GraphQLPath()),
			Types:         len(sum.Types),
			Queries:       len(sum.Queries),
			Mutations:     len(sum.Mutations),
			Subscriptions: len(sum.Subscriptions),
		}, nil
	})

	// --- gql_show ---
	mcp.AddTool(s, &mcp.Tool{
		Name:        "gql_show",
		Description: "List GraphQL schema types and root operations (queries, mutations, subscriptions) with arguments and return types.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, gql.Summary, error) {
		_, sch, err := openGQL(root)
		if err != nil {
			return nil, gql.Summary{}, err
		}
		return nil, sch.Summarize(), nil
	})

	// --- gql_query ---
	type GQLQueryIn struct {
		Query         string         `json:"query" jsonschema:"GraphQL query or mutation document"`
		OperationName string         `json:"operationName,omitempty" jsonschema:"operation name when the document defines several"`
		Variables     map[string]any `json:"variables,omitempty" jsonschema:"variable values (accepted but not used by the mock generator)"`
	}
	type GQLQueryOut struct {
		Status int `json:"status"`
		Body   any `json:"body"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "gql_query",
		Description: "Build a deterministic example response for a GraphQL query/mutation against the project's SDL schema. Returns the GraphQL envelope ({data, errors}) and the HTTP status the mock would return.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GQLQueryIn) (*mcp.CallToolResult, GQLQueryOut, error) {
		_, sch, err := openGQL(root)
		if err != nil {
			return nil, GQLQueryOut{}, err
		}
		body, status := sch.Example(in.Query, in.OperationName)
		return nil, GQLQueryOut{Status: status, Body: body}, nil
	})
}

// ---------------------------------------------------------------------------
// test tools
// ---------------------------------------------------------------------------

func registerTestTools(s *mcp.Server, root string) {
	type TestListOut struct {
		Tests []TestInfo `json:"tests"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_list",
		Description: "List test scenario files with name and step count.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, TestListOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, TestListOut{}, err
		}
		files, err := runner.Discover(p.TestsPath())
		if err != nil {
			return nil, TestListOut{}, err
		}
		out := TestListOut{Tests: []TestInfo{}}
		for _, f := range files {
			sc, err := runner.LoadScenario(f)
			if err != nil {
				out.Tests = append(out.Tests, TestInfo{File: filepath.Base(f), Name: "(unreadable)", Error: err.Error()})
				continue
			}
			out.Tests = append(out.Tests, TestInfo{
				File:  filepath.Base(f),
				Name:  sc.Name,
				Steps: len(sc.Steps),
				Skip:  sc.Skip,
			})
		}
		return nil, out, nil
	})

	type TestGetIn struct {
		File string `json:"file" jsonschema:"scenario file name inside the tests directory, e.g. get-pets.yaml"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_get",
		Description: "Read one test scenario file (parsed steps + raw YAML content).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TestGetIn) (*mcp.CallToolResult, TestGetOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, TestGetOut{}, err
		}
		path, err := safeTestPath(p, in.File)
		if err != nil {
			return nil, TestGetOut{}, err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, TestGetOut{}, err
		}
		sc, err := runner.LoadScenario(path)
		if err != nil {
			return nil, TestGetOut{}, err
		}
		return nil, TestGetOut{File: in.File, Content: string(content), Scenario: *sc}, nil
	})

	type TestCreateIn struct {
		Name        string        `json:"name" jsonschema:"human-readable scenario name"`
		Description string        `json:"description,omitempty"`
		Steps       []runner.Step `json:"steps" jsonschema:"ordered steps; each has request (method, path, headers, query, json/body) and expect (status, headers, json path assertions) and capture (name -> jsonpath)"`
		Overwrite   bool          `json:"overwrite,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_create",
		Description: "Create a test scenario YAML file. Steps support {{base_url}}, {{var}} and {{secret:NAME}} templates, JSONPath assertions ($.a.b[0]) with @exists/@not_exists/@null markers, and captures for chaining.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TestCreateIn) (*mcp.CallToolResult, TestCreateOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, TestCreateOut{}, err
		}
		sc := &runner.Scenario{Name: in.Name, Description: in.Description, Steps: in.Steps}
		file := runner.Slug(in.Name) + ".yaml"
		path := filepath.Join(p.TestsPath(), file)
		if !in.Overwrite {
			if _, err := os.Stat(path); err == nil {
				return nil, TestCreateOut{}, fmt.Errorf("%s already exists (set overwrite=true)", file)
			}
		}
		if err := runner.SaveScenario(sc, path); err != nil {
			return nil, TestCreateOut{}, err
		}
		return nil, TestCreateOut{OK: true, File: file, Message: fmt.Sprintf("scenario %q created with %d steps", in.Name, len(in.Steps))}, nil
	})

	type TestScaffoldIn struct {
		Method string `json:"method,omitempty" jsonschema:"restrict to one HTTP method"`
		Path   string `json:"path,omitempty" jsonschema:"restrict to a path prefix"`
		Force  bool   `json:"overwrite,omitempty" jsonschema:"overwrite existing test files"`
	}
	type TestScaffoldOut struct {
		OK        bool                    `json:"ok"`
		Generated []runner.ScaffoldResult `json:"generated"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_scaffold",
		Description: "Generate starter test scenarios for documented endpoints (one file per operation, seeded with examples from the spec).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TestScaffoldIn) (*mcp.CallToolResult, TestScaffoldOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, TestScaffoldOut{}, err
		}
		doc, err := openDoc(p)
		if err != nil {
			return nil, TestScaffoldOut{}, err
		}
		results, err := runner.ScaffoldAll(doc, p.TestsPath(), runner.ScaffoldOptions{
			Method: in.Method, Path: in.Path, Force: in.Force,
		})
		if err != nil {
			return nil, TestScaffoldOut{}, err
		}
		if results == nil {
			results = []runner.ScaffoldResult{}
		}
		return nil, TestScaffoldOut{OK: true, Generated: results}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_delete",
		Description: "Delete a test scenario file.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TestGetIn) (*mcp.CallToolResult, DeleteOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		path, err := safeTestPath(p, in.File)
		if err != nil {
			return nil, DeleteOut{}, err
		}
		if err := os.Remove(path); err != nil {
			return nil, DeleteOut{}, err
		}
		return nil, DeleteOut{OK: true, Message: in.File + " deleted"}, nil
	})

	type TestRunIn struct {
		Environment string `json:"environment,omitempty" jsonschema:"environment name; defaults to the active environment"`
		Filter      string `json:"filter,omitempty" jsonschema:"substring filter on file or scenario name"`
		FailFast    bool   `json:"failFast,omitempty" jsonschema:"stop at the first failing scenario"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_run",
		Description: "Run test scenarios against an environment. Requires MERAPI_SECRETS_KEY in the server environment when tests reference secrets.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TestRunIn) (*mcp.CallToolResult, runner.RunResult, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, runner.RunResult{}, err
		}
		envName := envs.Resolve(p, in.Environment)
		result, err := runTests(p, envName, in.Filter, in.FailFast)
		if err != nil {
			return nil, runner.RunResult{}, err
		}
		return nil, *result, nil
	})
}

type TestInfo struct {
	File  string `json:"file"`
	Name  string `json:"name"`
	Steps int    `json:"steps"`
	Skip  bool   `json:"skip,omitempty"`
	Error string `json:"error,omitempty"`
}

type TestGetOut struct {
	File     string          `json:"file"`
	Content  string          `json:"content"`
	Scenario runner.Scenario `json:"scenario"`
}

type TestCreateOut struct {
	OK      bool   `json:"ok"`
	File    string `json:"file"`
	Message string `json:"message"`
}

// runTests wires an environment into the runner.
func runTests(p *project.Project, envName, filter string, failFast bool) (*runner.RunResult, error) {
	env, err := envs.Load(p, envName)
	if err != nil {
		return nil, err
	}
	secrets, err := envs.LoadSecrets(p, envName)
	if err != nil {
		if !os.IsNotExist(err) && strings.Contains(err.Error(), envs.SecretsKeyEnvVar) {
			return nil, err
		}
		secrets = map[string]string{}
	}
	return runner.Run(p.TestsPath(), runner.Options{
		EnvName:   envName,
		BaseURL:   env.BaseURL,
		Variables: env.Variables,
		Secrets:   secrets,
		Filter:    filter,
		FailFast:  failFast,
		Timeout:   30 * time.Second,
	})
}

// safeTestPath resolves a scenario filename inside the tests directory and
// rejects traversal attempts.
func safeTestPath(p *project.Project, file string) (string, error) {
	base := filepath.Clean(strings.TrimSpace(file))
	if base == "" || base == "." || strings.HasPrefix(base, "..") || filepath.IsAbs(base) {
		return "", fmt.Errorf("invalid file name %q: pass a file name inside the tests directory", file)
	}
	path := filepath.Join(p.TestsPath(), base)
	rel, err := filepath.Rel(p.TestsPath(), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid file name %q", file)
	}
	return path, nil
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
