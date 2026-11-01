package cli

import (
	"fmt"
	"strings"

	"github.com/bilhasry/merapi/internal/spec"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "security",
		Aliases: []string{"sec"},
		Short:   "Manage API security schemes (components/securitySchemes)",
		Long: `Manage the API's authentication schemes (components/securitySchemes).

A scheme describes HOW clients authenticate (apiKey, http bearer/basic,
oauth2, openIdConnect); operations declare WHICH schemes they require via
` + "`--security`" + ` on endpoint add/update. The mock enforces these requirements
(missing credentials get the documented 401/403), and generated code samples
include the matching credential placeholders.`,
	}
	cmd.AddCommand(securityListCmd())
	cmd.AddCommand(securityAddCmd())
	cmd.AddCommand(securityDeleteCmd())
	cmd.AddCommand(securityGlobalCmd())
	return cmd
}

func securityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List security schemes and the operations using them",
		Example: `  merapi security list
  merapi security list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			schemes := doc.ListSecuritySchemes()
			using := doc.SecuritySchemesUsing()
			if flagJSON {
				if schemes == nil {
					schemes = []spec.SecurityScheme{}
				}
				usedBy := map[string][]string{}
				for name, ops := range using {
					usedBy[name] = ops
				}
				return printJSON(map[string]any{"schemes": schemes, "usedBy": usedBy})
			}
			if len(schemes) == 0 {
				info("No security schemes yet. Add one with `merapi security add BearerAuth --type http --scheme bearer`.")
				return nil
			}
			rows := make([][]string, 0, len(schemes))
			for _, s := range schemes {
				ops := using[s.Name]
				used := "-"
				if len(ops) > 0 {
					used = strings.Join(ops, ", ")
				}
				rows = append(rows, []string{s.Name, s.Describe(), truncate(s.Description, 40), truncate(used, 48)})
			}
			table([]string{"NAME", "TYPE", "DESCRIPTION", "USED BY"}, rows)
			return nil
		},
	}
}

func securityAddCmd() *cobra.Command {
	var (
		typ         string
		in          string
		keyName     string
		scheme      string
		bearerFmt   string
		description string
		flowsFile   string
		oidcURL     string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or replace a security scheme",
		Example: `  merapi security add BearerAuth --type http --scheme bearer --bearer-format JWT
  merapi security add ApiKeyAuth --type apiKey --in header --name X-API-Key
  merapi security add OAuth2 --type oauth2 --flows-file flows.yaml
  merapi security add OIDC --type openIdConnect --url https://issuer.example.com/.well-known/openid-configuration`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			s := spec.SecurityScheme{
				Name:             args[0],
				Type:             typ,
				In:               in,
				ParameterName:    keyName,
				Scheme:           scheme,
				BearerFormat:     bearerFmt,
				Description:      description,
				OpenIdConnectUrl: oidcURL,
			}
			if flowsFile != "" {
				data, err := readInput(flowsFile)
				if err != nil {
					return err
				}
				var flows map[string]any
				if err := unmarshalFlexible(data, &flows); err != nil {
					return fmt.Errorf("parsing flows file: %w", err)
				}
				s.Flows = flows
			}
			if err := doc.AddSecurityScheme(s); err != nil {
				return err
			}
			if err := doc.Save(); err != nil {
				return err
			}
			success("security scheme %s saved (%s)", s.Name, s.Describe())
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "scheme type: apiKey, http, oauth2, openIdConnect, mutualTLS")
	cmd.Flags().StringVar(&in, "in", "", "apiKey only: credential location (header, query, cookie)")
	cmd.Flags().StringVar(&keyName, "name", "", "apiKey only: credential parameter name (e.g. X-API-Key)")
	cmd.Flags().StringVar(&scheme, "scheme", "", "http only: auth scheme (bearer, basic, digest...)")
	cmd.Flags().StringVar(&bearerFmt, "bearer-format", "", "http only: token format hint (e.g. JWT)")
	cmd.Flags().StringVar(&description, "description", "", "human-readable description")
	cmd.Flags().StringVar(&flowsFile, "flows-file", "", "oauth2 only: YAML/JSON file with the flows object")
	cmd.Flags().StringVar(&oidcURL, "url", "", "openIdConnect only: discovery URL")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

func securityDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete a security scheme (refuses while operations still use it)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			if err := doc.DeleteSecurityScheme(args[0]); err != nil {
				return err
			}
			if err := doc.Save(); err != nil {
				return err
			}
			success("security scheme %s deleted", args[0])
			return nil
		},
	}
}

func securityGlobalCmd() *cobra.Command {
	var clear bool
	var setVal string
	cmd := &cobra.Command{
		Use:   "global",
		Short: "Show or set the document-level security (applies to operations without their own)",
		Example: `  merapi security global                        # show
  merapi security global --set BearerAuth       # every operation requires BearerAuth
  merapi security global --clear                # remove the document default`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			if clear {
				doc.SetSecurity(nil)
				if err := doc.Save(); err != nil {
					return err
				}
				success("document-level security removed")
				return nil
			}
			if cmd.Flags().Changed("set") {
				reqs, err := parseSecurityFlags([]string{setVal})
				if err != nil {
					return err
				}
				doc.SetSecurity(reqs)
				if err := doc.Save(); err != nil {
					return err
				}
				success("document-level security set (%s)", setVal)
				return nil
			}
			// Show current.
			reqs := doc.Security()
			if flagJSON {
				return printJSON(map[string]any{"security": reqs})
			}
			if len(reqs) == 0 {
				info("No document-level security set (operations are public unless they declare their own).")
				return nil
			}
			out, err := yaml.Marshal(map[string]any{"security": reqs})
			if err != nil {
				return err
			}
			fmt.Print(string(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&setVal, "set", "", "scheme requirement, e.g. BearerAuth or OAuth:read,write")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the document-level security")
	return cmd
}
