package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bilhasry/merapi/internal/spec"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newEndpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "endpoint",
		Aliases: []string{"ep"},
		Short:   "Manage API endpoints (OpenAPI operations)",
	}
	cmd.AddCommand(endpointListCmd())
	cmd.AddCommand(endpointGetCmd())
	cmd.AddCommand(endpointAddCmd())
	cmd.AddCommand(endpointUpdateCmd())
	cmd.AddCommand(endpointDeleteCmd())
	return cmd
}

func endpointListCmd() *cobra.Command {
	var tag, pathPrefix string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documented endpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			eps := doc.ListEndpoints(spec.EndpointFilter{PathPrefix: pathPrefix, Tag: tag})
			if flagJSON {
				if eps == nil {
					eps = []spec.EndpointSummary{}
				}
				return printJSON(map[string]any{"endpoints": eps})
			}
			if len(eps) == 0 {
				info("No endpoints documented yet. Add one with `merapi endpoint add GET /path --summary \"...\"`.")
				return nil
			}
			rows := make([][]string, 0, len(eps))
			for _, ep := range eps {
				rows = append(rows, []string{
					ep.Method,
					ep.Path,
					ep.OperationID,
					truncate(ep.Summary, 48),
					strings.Join(ep.Tags, ","),
					strings.Join(ep.Responses, ","),
				})
			}
			table([]string{"METHOD", "PATH", "OPERATION-ID", "SUMMARY", "TAGS", "RESPONSES"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag")
	cmd.Flags().StringVar(&pathPrefix, "path", "", "filter by path prefix")
	return cmd
}

func endpointGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <METHOD> <path>",
		Short: "Show the full documentation of one endpoint",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			ep := doc.GetEndpoint(args[0], args[1])
			if ep == nil {
				return fmt.Errorf("%s %s not found", strings.ToUpper(args[0]), args[1])
			}
			if flagJSON {
				return printJSON(ep)
			}
			out, err := yaml.Marshal(ep)
			if err != nil {
				return err
			}
			fmt.Print(string(out))
			return nil
		},
	}
}

// endpointFlagSet registers the shared add/update flags.
type endpointFlags struct {
	operationID    string
	summary        string
	description    string
	tags           []string
	deprecated     bool
	requestBody    string
	requestExample string
	responses      []string
	params         []string
	security       []string
	securitySet    bool
	file           string
}

func (ef *endpointFlags) register(cmd *cobra.Command, updating bool) {
	cmd.Flags().StringVar(&ef.operationID, "operation-id", "", "camelCase operationId")
	cmd.Flags().StringVar(&ef.summary, "summary", "", "one-line summary")
	cmd.Flags().StringVar(&ef.description, "description", "", "detailed description")
	cmd.Flags().StringSliceVar(&ef.tags, "tag", nil, "tag (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&ef.deprecated, "deprecated", false, "mark the endpoint deprecated")
	cmd.Flags().StringVar(&ef.requestBody, "request-body", "", "request body: schema name (e.g. Pet) or @file.json for an inline example")
	cmd.Flags().StringVar(&ef.requestExample, "example", "", "request example: JSON string or @file.json")
	cmd.Flags().StringArrayVar(&ef.responses, "response", nil, "response as CODE[:DESCRIPTION[:SCHEMA]] (repeatable), e.g. --response 200:OK:Pet")
	cmd.Flags().StringArrayVar(&ef.params, "param", nil, "parameter as NAME:IN[:REQUIRED] (repeatable), e.g. --param id:path:required")
	cmd.Flags().StringArrayVar(&ef.security, "security", nil, "security requirement as SCHEME[:SCOPE1,SCOPE2] (repeatable, OR semantics); --security \"\" makes the endpoint public")
	cmd.Flags().BoolVar(&ef.securitySet, "public", false, "make the endpoint explicitly public (no auth), overriding document-level security")
	cmd.Flags().StringVarP(&ef.file, "file", "f", "", "read the endpoint definition from a YAML/JSON file ('-' for stdin)")
}

// buildEndpoint assembles an Endpoint from flags and/or a definition file.
func (ef *endpointFlags) build(method, path string) (*spec.Endpoint, error) {
	ep := &spec.Endpoint{}
	if ef.file != "" {
		data, err := readInput(ef.file)
		if err != nil {
			return nil, err
		}
		if err := unmarshalFlexible(data, ep); err != nil {
			return nil, fmt.Errorf("parsing endpoint definition: %w", err)
		}
	}
	if method != "" {
		ep.Method = method
	}
	if path != "" {
		ep.Path = path
	}
	applyIfSet := func(dst *string, src string) {
		if src != "" {
			*dst = src
		}
	}
	applyIfSet(&ep.OperationID, ef.operationID)
	applyIfSet(&ep.Summary, ef.summary)
	applyIfSet(&ep.Description, ef.description)
	if len(ef.tags) > 0 {
		ep.Tags = ef.tags
	}
	if ef.deprecated {
		ep.Deprecated = true
	}
	if ef.requestBody != "" || ef.requestExample != "" {
		rb := ep.RequestBody
		if rb == nil {
			rb = &spec.RequestBody{}
		}
		if strings.HasPrefix(ef.requestBody, "@") || strings.HasPrefix(ef.requestBody, "{") {
			example, err := parseJSONInput(ef.requestBody)
			if err != nil {
				return nil, fmt.Errorf("--request-body: %w", err)
			}
			rb.Example = example
		} else if ef.requestBody != "" {
			rb.SchemaRef = ef.requestBody
		}
		if ef.requestExample != "" {
			example, err := parseJSONInput(ef.requestExample)
			if err != nil {
				return nil, fmt.Errorf("--example: %w", err)
			}
			rb.Example = example
		}
		ep.RequestBody = rb
	}
	if len(ef.responses) > 0 {
		responses := map[string]spec.Response{}
		for _, r := range ef.responses {
			parts := strings.SplitN(r, ":", 3)
			code := parts[0]
			if !isStatusCode(code) {
				return nil, fmt.Errorf("--response: invalid status code %q", code)
			}
			resp := spec.Response{}
			if len(parts) > 1 {
				resp.Description = parts[1]
			}
			if len(parts) > 2 {
				resp.SchemaRef = parts[2]
			}
			resp.ContentType = "application/json"
			responses[code] = resp
		}
		ep.Responses = responses
	}
	if len(ef.params) > 0 {
		var params []spec.Parameter
		for _, p := range ef.params {
			parts := strings.Split(p, ":")
			if len(parts) < 2 {
				return nil, fmt.Errorf("--param %q: expected NAME:IN[:required]", p)
			}
			param := spec.Parameter{Name: parts[0], In: parts[1]}
			if len(parts) > 2 && strings.EqualFold(parts[2], "required") {
				param.Required = true
			}
			switch param.In {
			case "path":
				param.Required = true
				param.Schema = map[string]any{"type": "string"}
			case "query", "header", "cookie":
				param.Schema = map[string]any{"type": "string"}
			default:
				return nil, fmt.Errorf("--param %q: invalid location %q (path, query, header, cookie)", p, param.In)
			}
			params = append(params, param)
		}
		ep.Parameters = params
	}
	if ef.securitySet {
		ep.Security = []map[string][]string{} // explicitly public (--public)
	} else if ef.security != nil {
		reqs, err := parseSecurityFlags(ef.security)
		if err != nil {
			return nil, err
		}
		ep.Security = reqs
	}
	if ep.Method == "" || ep.Path == "" {
		return nil, fmt.Errorf("method and path are required (positional args or --file)")
	}
	return ep, nil
}

// parseSecurityFlags turns --security values into OpenAPI requirements:
// "BearerAuth" -> {BearerAuth: []}, "OAuth:read,write" -> {OAuth: [read write]}.
// An empty list value means "explicitly public".
func parseSecurityFlags(values []string) ([]map[string][]string, error) {
	if len(values) == 0 {
		return []map[string][]string{}, nil
	}
	var reqs []map[string][]string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue // `--security ""` contributes nothing; use --public for clarity
		}
		name, scopes := v, []string{}
		if i := strings.Index(v, ":"); i >= 0 {
			name = strings.TrimSpace(v[:i])
			if s := strings.TrimSpace(v[i+1:]); s != "" {
				scopes = strings.Split(s, ",")
				for i := range scopes {
					scopes[i] = strings.TrimSpace(scopes[i])
				}
			}
		}
		if name == "" {
			return nil, fmt.Errorf("--security %q: expected SCHEME[:SCOPE1,SCOPE2]", v)
		}
		reqs = append(reqs, map[string][]string{name: scopes})
	}
	if reqs == nil {
		reqs = []map[string][]string{}
	}
	return reqs, nil
}

func endpointAddCmd() *cobra.Command {
	var ef endpointFlags
	cmd := &cobra.Command{
		Use:   "add <METHOD> <path>",
		Short: "Document a new endpoint",
		Long: `Document a new endpoint in the OpenAPI document.

Examples:
  merapi endpoint add GET /pets --summary "List pets" --tag pets
  merapi endpoint add POST /pets --request-body Pet --response 201:Created:Pet
  merapi endpoint add GET /pets/{id} --param id:path:required --summary "Get a pet"
  merapi endpoint add -f endpoint.yaml        # full definition from a file`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var method, path string
			if len(args) == 2 {
				method, path = args[0], args[1]
			}
			ep, err := ef.build(method, path)
			if err != nil {
				return err
			}
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			if err := doc.AddEndpoint(ep); err != nil {
				return err
			}
			if err := doc.Save(); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "operationId": ep.OperationID, "method": ep.Method, "path": ep.Path})
			}
			success("Documented %s %s (operationId: %s)", ep.Method, ep.Path, ep.OperationID)
			return nil
		},
	}
	ef.register(cmd, false)
	return cmd
}

func endpointUpdateCmd() *cobra.Command {
	var ef endpointFlags
	cmd := &cobra.Command{
		Use:   "update <METHOD> <path>",
		Short: "Update an existing endpoint (only provided fields change)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, err := ef.build(args[0], args[1])
			if err != nil {
				return err
			}
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			deprecatedSet := cmd.Flags().Changed("deprecated")
			if err := doc.UpdateEndpoint(ep, deprecatedSet); err != nil {
				return err
			}
			if err := doc.Save(); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "method": ep.Method, "path": ep.Path})
			}
			success("Updated %s %s", ep.Method, ep.Path)
			return nil
		},
	}
	ef.register(cmd, true)
	return cmd
}

func endpointDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <METHOD> <path>",
		Short: "Remove an endpoint from the document",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			if err := doc.DeleteEndpoint(args[0], args[1]); err != nil {
				return err
			}
			if err := doc.Save(); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true})
			}
			success("Deleted %s %s", strings.ToUpper(args[0]), args[1])
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// shared input helpers
// ---------------------------------------------------------------------------

func readInput(file string) ([]byte, error) {
	if file == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(file)
}

func unmarshalFlexible(data []byte, v any) error {
	if err := yaml.Unmarshal(data, v); err == nil {
		return nil
	}
	return json.Unmarshal(data, v)
}

func parseJSONInput(s string) (any, error) {
	if strings.HasPrefix(s, "@") {
		data, err := os.ReadFile(strings.TrimPrefix(s, "@"))
		if err != nil {
			return nil, err
		}
		s = string(data)
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		// Allow YAML too.
		if err2 := yaml.Unmarshal([]byte(s), &v); err2 == nil {
			return v, nil
		}
		return nil, fmt.Errorf("not valid JSON or YAML: %v", err)
	}
	return v, nil
}

func isStatusCode(code string) bool {
	if code == "default" {
		return true
	}
	if len(code) != 3 {
		return false
	}
	if code[0] < '1' || code[0] > '5' {
		return false
	}
	for i := 1; i < 3; i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
