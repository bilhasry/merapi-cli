package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/bilhasry/merapi/internal/project"
	"github.com/bilhasry/merapi/internal/runner"
	"github.com/bilhasry/merapi/internal/templ"
	"github.com/spf13/cobra"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "test",
		Aliases: []string{"t"},
		Short:   "Create, inspect and run API test scenarios",
	}
	cmd.AddCommand(testListCmd())
	cmd.AddCommand(testShowCmd())
	cmd.AddCommand(testNewCmd())
	cmd.AddCommand(testScaffoldCmd())
	cmd.AddCommand(testRunCmd())
	cmd.AddCommand(testDeleteCmd())
	return cmd
}

func testListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List test scenarios",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			files, err := runner.Discover(p.TestsPath())
			if err != nil {
				return err
			}
			type row struct {
				File  string `json:"file"`
				Name  string `json:"name"`
				Steps int    `json:"steps"`
				Skip  bool   `json:"skip"`
			}
			var rows []row
			var tableRows [][]string
			for _, f := range files {
				sc, err := runner.LoadScenario(f)
				if err != nil {
					rows = append(rows, row{File: filepath.Base(f), Name: "(unreadable)"})
					tableRows = append(tableRows, []string{filepath.Base(f), "(unreadable)", "0", ""})
					continue
				}
				rows = append(rows, row{File: filepath.Base(f), Name: sc.Name, Steps: len(sc.Steps), Skip: sc.Skip})
				skip := ""
				if sc.Skip {
					skip = "yes"
				}
				tableRows = append(tableRows, []string{filepath.Base(f), sc.Name, fmt.Sprintf("%d", len(sc.Steps)), skip})
			}
			if flagJSON {
				if rows == nil {
					rows = []row{}
				}
				return printJSON(map[string]any{"tests": rows})
			}
			if len(rows) == 0 {
				info("No test scenarios yet. Generate them from the docs with `merapi test scaffold --all`.")
				return nil
			}
			table([]string{"FILE", "NAME", "STEPS", "SKIP"}, tableRows)
			return nil
		},
	}
}

func testShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <file>",
		Short: "Print a test scenario file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			path, err := testPath(p, args[0])
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Print(string(data))
			return nil
		},
	}
}

func testNewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new <name>",
		Short: "Create a new empty test scenario skeleton",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := args[0]
			file := runner.Slug(name) + ".yaml"
			path := filepath.Join(p.TestsPath(), file)
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists", file)
			}
			sc := &runner.Scenario{
				Name: name,
				Steps: []runner.Step{{
					Name:    "example",
					Request: runner.Request{Method: "GET", Path: "/"},
					Expect:  runner.Expect{Status: 200},
				}},
			}
			if err := runner.SaveScenario(sc, path); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "file": file})
			}
			success("Created %s — edit it, then run `merapi test run --filter %s`", file, file)
			return nil
		},
	}
}

func testScaffoldCmd() *cobra.Command {
	var (
		method string
		path   string
		force  bool
		all    bool
	)
	cmd := &cobra.Command{
		Use:   "scaffold [METHOD] [path-prefix]",
		Short: "Generate starter test scenarios from documented endpoints",
		Long: `Generate a starter test scenario for each documented endpoint:
expected status, content-type assertions, request examples from the spec, and
{{variable}} placeholders for path parameters.

With no arguments (or --all) every endpoint gets a test; pass METHOD or a path
prefix to narrow it:  merapi test scaffold GET /pets`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = all
			if len(args) >= 1 {
				method = args[0]
			}
			if len(args) >= 2 {
				path = args[1]
			}
			p, doc, err := mustDoc()
			if err != nil {
				return err
			}
			results, err := runner.ScaffoldAll(doc, p.TestsPath(), runner.ScaffoldOptions{
				Method: method, Path: path, Force: force,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"generated": results})
			}
			if len(results) == 0 {
				info("No endpoints to scaffold. Document one with `merapi endpoint add GET /path --summary \"...\"` first.")
				return nil
			}
			for _, r := range results {
				if r.Skipped {
					info("  - %s %s: skipped (%s)", r.Method, r.Path, r.Reason)
				} else {
					success("  - %s %s: %s", r.Method, r.Path, filepath.Base(r.File))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing test files")
	cmd.Flags().BoolVar(&all, "all", false, "scaffold every documented endpoint (default)")
	return cmd
}

func testRunCmd() *cobra.Command {
	var (
		filter   string
		failFast bool
		timeout  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run test scenarios against an environment",
		Long: `Run every test scenario (or those matching --filter) against the selected
environment. Templates like {{base_url}}, {{var}} and {{secret:NAME}} are resolved
from the environment; captured values chain into later steps.

Secrets require the MERAPI_SECRETS_KEY environment variable to be set.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			envName := envs.Resolve(p, flagEnv)
			env, err := envs.Load(p, envName)
			if err != nil {
				return err
			}
			secrets, err := envs.LoadSecrets(p, envName)
			if err != nil {
				// Only fatal when secrets are actually referenced; warn otherwise.
				if strings.Contains(err.Error(), envs.SecretsKeyEnvVar) {
					if usesSecrets(p.TestsPath()) {
						return err
					}
					secrets = map[string]string{}
					warn("%v", err)
				} else {
					return err
				}
			}
			result, err := runner.Run(p.TestsPath(), runner.Options{
				EnvName:   envName,
				BaseURL:   env.BaseURL,
				Variables: env.Variables,
				Secrets:   secrets,
				Filter:    filter,
				FailFast:  failFast,
				Timeout:   timeout,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				if err := printJSON(result); err != nil {
					return err
				}
			} else {
				printRunResult(result)
			}
			if !result.Success() {
				return fmt.Errorf("%d scenario(s) failed", result.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&filter, "filter", "", "only run scenarios whose file or name contains this substring")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, "stop at the first failing scenario")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "per-request timeout")
	return cmd
}

func printRunResult(result *runner.RunResult) {
	info("Environment: %s", result.Env)
	info("")
	for _, sc := range result.Scenarios {
		status := paint(ansiGreen, "PASS")
		switch {
		case sc.Skipped:
			status = paint(ansiDim, "SKIP")
		case !sc.Passed:
			status = paint(ansiRed, "FAIL")
		}
		fmt.Printf("%s  %s  %s\n", status, sc.Name, paint(ansiDim, fmt.Sprintf("(%dms)", sc.DurationMS)))
		if sc.Error != "" {
			fmt.Printf("      %s\n", paint(ansiRed, sc.Error))
		}
		for _, step := range sc.Steps {
			if step.Skipped {
				continue
			}
			mark := paint(ansiGreen, "✓")
			if !step.Passed {
				mark = paint(ansiRed, "✗")
			}
			label := step.Name
			if label == "" {
				label = step.Request
			}
			fmt.Printf("      %s %s %s\n", mark, label, paint(ansiDim, fmt.Sprintf("%d %dms", step.Status, step.DurationMS)))
			for _, f := range step.Failures {
				fmt.Printf("          %s\n", paint(ansiRed, f))
			}
			if !step.Passed && step.BodyExcerpt != "" {
				fmt.Printf("          body: %s\n", paint(ansiDim, step.BodyExcerpt))
			}
		}
	}
	info("")
	summary := fmt.Sprintf("%d passed, %d failed, %d skipped in %dms",
		result.Passed, result.Failed, result.Skipped, result.DurationMS)
	if result.Success() {
		success("%s", summary)
	} else {
		errorf("%s", summary)
	}
}

func testDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <file>",
		Short: "Delete a test scenario file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			path, err := testPath(p, args[0])
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true})
			}
			success("Deleted %s", args[0])
			return nil
		},
	}
}

// testPath resolves a scenario filename inside the tests directory, rejecting
// traversal.
func testPath(p *project.Project, file string) (string, error) {
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

// usesSecrets reports whether any scenario references {{secret:...}}.
func usesSecrets(testsDir string) bool {
	files, err := runner.Discover(testsDir)
	if err != nil {
		return false
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if len(templ.References(string(data))) > 0 {
			for _, ref := range templ.References(string(data)) {
				if strings.HasPrefix(ref, "secret:") {
					return true
				}
			}
		}
	}
	return false
}
