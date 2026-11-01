// Package cli wires the merapi command-line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/bilhasry/merapi/internal/project"
	"github.com/bilhasry/merapi/internal/spec"
	"github.com/spf13/cobra"
)

// Version is the CLI version (overridable via -ldflags).
var Version = "0.1.0"

var (
	flagProject string
	flagEnv     string
	flagJSON    bool
	flagQuiet   bool
)

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	root := Build()
	if err := root.Execute(); err != nil {
		if _, ok := err.(validationError); !ok {
			errorf("%v", err)
		}
		return 1
	}
	return 0
}

// Build constructs the merapi command tree. Exposed so tests and embedders
// (cmd/merapi, or programs driving merapi programmatically) can obtain the
// full CLI without executing a process.
func Build() *cobra.Command {
	root := &cobra.Command{
		Use:   "merapi",
		Short: "API management from the terminal",
		Long: `merapi manages API documentation (OpenAPI 3.1), test scenarios and
environments (variables + encrypted secrets) from the terminal — no UI needed.

AI agents connect through the built-in MCP server: run "merapi mcp" and point
any MCP-capable client at it to maintain documentation and tests.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&flagProject, "project", "C", "", "project directory (default: search upward from the current directory)")
	root.PersistentFlags().StringVar(&flagEnv, "env", "", "environment name (default: the active environment)")
	root.PersistentFlags().BoolVarP(&flagJSON, "json", "j", false, "output machine-readable JSON")
	root.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "only print errors")

	root.AddCommand(newInitCmd())
	root.AddCommand(newEndpointCmd())
	root.AddCommand(newSchemaCmd())
	root.AddCommand(newSecurityCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newDocCmd())
	root.AddCommand(newGQLCmd())
	root.AddCommand(newMockCmd())
	root.AddCommand(newTestCmd())
	root.AddCommand(newEnvCmd())
	root.AddCommand(newVarCmd())
	root.AddCommand(newSecretCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newVersionCmd())
	return root
}

// mustProject resolves the project or returns a helpful error.
func mustProject() (*project.Project, error) {
	start := flagProject
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		start = wd
	}
	return project.Find(start)
}

// mustDoc loads the OpenAPI document of the current project.
func mustDoc() (*project.Project, *spec.Document, error) {
	p, err := mustProject()
	if err != nil {
		return nil, nil, err
	}
	doc, err := spec.Load(p.SpecPath())
	if err != nil {
		return nil, nil, fmt.Errorf("%s\nRun `merapi init` in a new directory, or `merapi endpoint add` after initializing.", err)
	}
	return p, doc, nil
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the merapi version",
		Run: func(cmd *cobra.Command, args []string) {
			if flagJSON {
				_ = printJSON(map[string]string{"name": "merapi", "version": Version})
				return
			}
			fmt.Printf("merapi %s\n", Version)
		},
	}
}
