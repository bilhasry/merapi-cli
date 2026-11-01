package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bilhasry/merapi/internal/gql"
	"github.com/bilhasry/merapi/internal/project"
	"github.com/spf13/cobra"
)

func newGQLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gql",
		Short: "Work with the project's GraphQL SDL schema",
		Long: `Work with the project's GraphQL SDL schema (schema.graphql by default).

A project may document REST (openapi.yaml), GraphQL (schema.graphql), or both.
GraphQL support is additive: REST commands are unaffected. See
docs/design/graphql.md for the design.`,
	}
	cmd.AddCommand(gqlValidateCmd())
	cmd.AddCommand(gqlShowCmd())
	return cmd
}

// mustGQL loads the project's SDL file or returns a helpful error.
func mustGQL() (*project.Project, *gql.Schema, error) {
	p, err := mustProject()
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

func gqlValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the GraphQL SDL schema (exit 1 on errors)",
		Example: `  merapi gql validate
  merapi gql validate --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, s, err := mustGQL()
			if err != nil {
				return err
			}
			sum := s.Summarize()
			if flagJSON {
				return printJSON(map[string]any{
					"ok":            true,
					"file":          relPath(p, p.GraphQLPath()),
					"types":         len(sum.Types),
					"queries":       len(sum.Queries),
					"mutations":     len(sum.Mutations),
					"subscriptions": len(sum.Subscriptions),
				})
			}
			success("%s is a valid GraphQL schema (%d types, %d queries, %d mutations)",
				relPath(p, p.GraphQLPath()), len(sum.Types), len(sum.Queries), len(sum.Mutations))
			return nil
		},
	}
}

func gqlShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show schema types and root operations",
		Example: `  merapi gql show
  merapi gql show --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := mustGQL()
			if err != nil {
				return err
			}
			sum := s.Summarize()
			if flagJSON {
				return printJSON(sum)
			}
			if len(sum.Queries) > 0 {
				fmt.Println("QUERIES")
				table([]string{"NAME", "ARGUMENTS", "RETURNS"}, opRows(sum.Queries))
				fmt.Println()
			}
			if len(sum.Mutations) > 0 {
				fmt.Println("MUTATIONS")
				table([]string{"NAME", "ARGUMENTS", "RETURNS"}, opRows(sum.Mutations))
				fmt.Println()
			}
			if len(sum.Subscriptions) > 0 {
				fmt.Println("SUBSCRIPTIONS")
				table([]string{"NAME", "ARGUMENTS", "RETURNS"}, opRows(sum.Subscriptions))
				fmt.Println()
			}
			fmt.Println("TYPES")
			rows := make([][]string, 0, len(sum.Types))
			for _, t := range sum.Types {
				rows = append(rows, []string{t.Name, t.Kind, fmt.Sprintf("%d", t.FieldCount), t.Description})
			}
			table([]string{"NAME", "KIND", "FIELDS", "DESCRIPTION"}, rows)
			return nil
		},
	}
}

func opRows(ops []gql.Operation) [][]string {
	rows := make([][]string, 0, len(ops))
	for _, op := range ops {
		rows = append(rows, []string{op.Name, formatArgs(op.Arguments), op.ReturnType})
	}
	return rows
}

func formatArgs(args []gql.Argument) string {
	if len(args) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		s := a.Name + ": " + a.Type
		if a.Required && !strings.HasSuffix(s, "!") {
			s += "!"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func relPath(p *project.Project, path string) string {
	if rel, err := filepath.Rel(p.Root, path); err == nil {
		return rel
	}
	return path
}
