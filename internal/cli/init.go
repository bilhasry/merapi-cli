package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/bilhasry/merapi/internal/project"
	"github.com/bilhasry/merapi/internal/spec"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var (
		force     bool
		title     string
		serverURL string
		envName   string
	)
	cmd := &cobra.Command{
		Use:   "init [directory]",
		Short: "Initialize a new merapi project",
		Long: `Initialize a new merapi project in the given directory (default: "."):

  merapi.yaml           project configuration
  openapi.yaml          OpenAPI 3.1 document skeleton
  environments/dev.yaml first environment
  tests/                test scenarios directory
  .gitignore            extended with local artifacts (.merapi/, dist/)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			if title == "" {
				abs, _ := filepath.Abs(dir)
				title = filepath.Base(abs)
			}
			if envName == "" {
				envName = "dev"
			}
			p, err := project.Init(dir, title, force)
			if err != nil {
				return err
			}

			// Skeleton OpenAPI document (unless one exists and !force).
			specPath := p.SpecPath()
			if _, err := os.Stat(specPath); err != nil || force {
				doc := spec.NewSkeleton(specPath, title, "0.1.0", fmt.Sprintf("API documentation for %s, managed by merapi.", title), serverURL)
				if err := doc.Save(); err != nil {
					return err
				}
			}

			// First environment.
			if _, err := envs.Create(p, envName, serverURL, false); err != nil {
				warn("%v", err)
			}

			if flagJSON {
				return printJSON(map[string]any{
					"ok":          true,
					"root":        p.Root,
					"spec":        specPath,
					"environment": envName,
				})
			}
			success("Initialized merapi project %q in %s", p.Config.Name, p.Root)
			info("")
			info("Next steps:")
			info("  merapi schema add Pet --property id:integer --property name:string --required id,name")
			info("  merapi endpoint add GET /pets --summary \"List pets\"")
			info("  merapi validate")
			info("  merapi test scaffold --all")
			info("  merapi env add staging --base-url https://api.staging.example.com")
			info("  merapi mcp            # let AI agents manage this project")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	cmd.Flags().StringVar(&title, "title", "", "API title (default: directory name)")
	cmd.Flags().StringVar(&serverURL, "server", "http://localhost:8080", "default server base URL")
	cmd.Flags().StringVar(&envName, "environment", "dev", "name of the first environment")
	return cmd
}
