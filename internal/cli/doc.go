package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bilhasry/merapi/internal/docs"
	"github.com/bilhasry/merapi/internal/envs"
	"github.com/spf13/cobra"
)

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Render and preview API documentation",
	}
	cmd.AddCommand(docExportCmd())
	cmd.AddCommand(docPreviewCmd())
	cmd.AddCommand(docMarkdownCmd())
	cmd.AddCommand(docSamplesCmd())
	return cmd
}

func docExportCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export interactive API documentation as a standalone HTML file (Swagger UI)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, d, err := mustDoc()
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(p.SpecPath())
			if err != nil {
				return err
			}
			specJSON, err := docs.ToJSON(raw)
			if err != nil {
				return err
			}
			page, err := docs.HTML(specJSON, d.Title(), "exported by <code>merapi doc export</code>")
			if err != nil {
				return err
			}
			if out == "" {
				out = filepath.Join("dist", "index.html")
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(out, page, 0o644); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "file": out})
			}
			success("Exported interactive docs to %s", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "output file (default: dist/index.html)")
	return cmd
}

func docPreviewCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Serve interactive API documentation on a local port (live-reloads on refresh)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, d, err := mustDoc()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return docs.Serve(ctx, p.SpecPath(), d.Title(), addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8085", "address to listen on")
	return cmd
}

func docMarkdownCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "markdown",
		Short: "Generate a Markdown API reference from the OpenAPI document",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, d, err := mustDoc()
			if err != nil {
				return err
			}
			content := docs.Markdown(d)
			if out == "" || out == "-" {
				fmt.Print(string(content))
				return nil
			}
			if err := os.WriteFile(out, content, 0o644); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "file": out})
			}
			success("Wrote %s", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "output file (default: stdout)")
	return cmd
}

// activeBaseURL returns the base URL of the active (or --env) environment,
// or "" when the project has no environments yet.
func activeBaseURL() string {
	p, err := mustProject()
	if err != nil {
		return ""
	}
	name := envs.Resolve(p, flagEnv)
	env, err := envs.Load(p, name)
	if err != nil {
		return ""
	}
	return env.BaseURL
}

func docSamplesCmd() *cobra.Command {
	var lang string
	var baseURL string
	cmd := &cobra.Command{
		Use:   "samples <METHOD> <path>",
		Short: "Print ready-to-paste request samples (cURL, Go, Python, JavaScript)",
		Long: `Print ready-to-paste request samples for one documented endpoint.

Samples use the active environment's base URL (or --base-url) and are derived
from the operation's parameters, request body and schema examples.`,
		Example: `  merapi doc samples GET /pets
  merapi doc samples POST /pets --lang go,python
  merapi doc samples GET /pets/{id} --base-url https://api.staging.example.com`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, d, err := mustDoc()
			if err != nil {
				return err
			}
			ep := d.GetEndpoint(args[0], args[1])
			if ep == nil {
				return fmt.Errorf("%s %s not found in the document — list endpoints with `merapi endpoint list`",
					strings.ToUpper(args[0]), args[1])
			}
			var langs []string
			if lang != "" {
				for _, l := range strings.Split(lang, ",") {
					if l = strings.TrimSpace(l); l != "" {
						langs = append(langs, l)
					}
				}
			}
			if baseURL == "" {
				baseURL = activeBaseURL()
			}
			samples, err := docs.Samples(d, ep, baseURL, langs)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{
					"method":  strings.ToUpper(ep.Method),
					"path":    ep.Path,
					"baseUrl": docs.BaseURL(d, baseURL),
					"samples": samples,
				})
			}
			for i, s := range samples {
				if i > 0 {
					fmt.Println()
				}
				fmt.Printf("### %s\n\n```%s\n%s```\n", docs.SampleDisplayName(s.Language), docs.SampleFence(s.Language), s.Code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&lang, "lang", "", "comma-separated languages: curl, go, python, javascript (default: all)")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "base URL for the samples (default: active environment's base_url)")
	return cmd
}
