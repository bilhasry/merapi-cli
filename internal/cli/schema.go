package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bilhasry/merapi/internal/spec"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schema",
		Aliases: []string{"sch"},
		Short:   "Manage reusable schemas (components/schemas)",
	}
	cmd.AddCommand(schemaListCmd())
	cmd.AddCommand(schemaGetCmd())
	cmd.AddCommand(schemaAddCmd())
	cmd.AddCommand(schemaDeleteCmd())
	return cmd
}

func schemaListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List reusable schemas",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			schemas := doc.ListSchemas()
			if flagJSON {
				if schemas == nil {
					schemas = []spec.SchemaSummary{}
				}
				return printJSON(map[string]any{"schemas": schemas})
			}
			if len(schemas) == 0 {
				info("No schemas yet. Add one with `merapi schema add Pet --property id:integer --property name:string`.")
				return nil
			}
			rows := make([][]string, 0, len(schemas))
			for _, s := range schemas {
				rows = append(rows, []string{s.Name, orDash(s.Type), strconv.Itoa(s.Properties), strings.Join(s.Required, ","), truncate(s.Description, 40)})
			}
			table([]string{"NAME", "TYPE", "PROPS", "REQUIRED", "DESCRIPTION"}, rows)
			return nil
		},
	}
}

func schemaGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Print one schema definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			schema := doc.GetSchema(args[0])
			if schema == nil {
				return fmt.Errorf("schema %q not found", args[0])
			}
			out, err := yaml.Marshal(schema)
			if err != nil {
				return err
			}
			fmt.Print(string(out))
			return nil
		},
	}
}

func schemaAddCmd() *cobra.Command {
	var (
		typ        string
		properties []string
		required   string
		schemaFile string
		force      bool
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or replace a reusable schema",
		Long: `Add a reusable schema under components/schemas.

Examples:
  merapi schema add Pet --property id:integer --property name:string --required id,name
  merapi schema add Error --type object --property code:integer --property message:string
  merapi schema add Pet -f pet-schema.yaml --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			var schema map[string]any
			if schemaFile != "" {
				data, err := readInput(schemaFile)
				if err != nil {
					return err
				}
				if err := unmarshalFlexible(data, &schema); err != nil {
					return fmt.Errorf("parsing schema: %w", err)
				}
			} else {
				if len(properties) == 0 {
					return fmt.Errorf("provide --property key:type (repeatable) or -f schema.yaml")
				}
				if typ == "" {
					typ = "object"
				}
				props := map[string]any{}
				for _, p := range properties {
					key, typ, err := parseProperty(p)
					if err != nil {
						return err
					}
					props[key] = map[string]any{"type": typ}
				}
				schema = map[string]any{"type": typ, "properties": props}
				if required != "" {
					var req []string
					for _, r := range strings.Split(required, ",") {
						if r = strings.TrimSpace(r); r != "" {
							req = append(req, r)
						}
					}
					schema["required"] = req
				}
			}
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			if err := doc.AddSchema(name, schema, force); err != nil {
				return err
			}
			if err := doc.Save(); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "name": name})
			}
			success("Schema %s saved", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "object", "schema type (object, string, number, integer, boolean, array)")
	cmd.Flags().StringArrayVar(&properties, "property", nil, "property as KEY:TYPE (repeatable)")
	cmd.Flags().StringVar(&required, "required", "", "comma-separated required property names")
	cmd.Flags().StringVarP(&schemaFile, "file", "f", "", "read the schema from a YAML/JSON file ('-' for stdin)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing schema")
	return cmd
}

func schemaDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a schema (refused while operations still reference it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, doc, err := mustDoc()
			if err != nil {
				return err
			}
			if err := doc.DeleteSchema(args[0]); err != nil {
				return err
			}
			if err := doc.Save(); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true})
			}
			success("Schema %s deleted", args[0])
			return nil
		},
	}
}

func parseProperty(p string) (string, string, error) {
	parts := strings.SplitN(p, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid --property %q: expected KEY:TYPE", p)
	}
	return parts[0], parts[1], nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
