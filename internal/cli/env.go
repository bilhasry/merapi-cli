package cli

import (
	"fmt"
	"sort"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "env",
		Aliases: []string{"environment"},
		Short:   "Manage environments (base URLs, variables, secrets)",
	}
	cmd.AddCommand(envListCmd())
	cmd.AddCommand(envShowCmd())
	cmd.AddCommand(envAddCmd())
	cmd.AddCommand(envUseCmd())
	cmd.AddCommand(envDeleteCmd())
	return cmd
}

func envListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List environments",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			names, err := envs.List(p)
			if err != nil {
				return err
			}
			type row struct {
				Name      string `json:"name"`
				BaseURL   string `json:"baseUrl,omitempty"`
				Variables int    `json:"variables"`
				Secrets   int    `json:"secrets"`
				Active    bool   `json:"active"`
			}
			var rows []row
			var tableRows [][]string
			for _, name := range names {
				r := row{Name: name}
				if env, err := envs.Load(p, name); err == nil {
					r.BaseURL = env.BaseURL
					r.Variables = len(env.Variables)
				}
				if secrets, err := envs.LoadSecrets(p, name); err == nil {
					r.Secrets = len(secrets)
				}
				r.Active = p.ActiveEnv() == name
				rows = append(rows, r)
				marker := ""
				if r.Active {
					marker = paint(ansiGreen, "●")
				}
				tableRows = append(tableRows, []string{marker, name, r.BaseURL, fmt.Sprintf("%d", r.Variables), fmt.Sprintf("%d", r.Secrets)})
			}
			if flagJSON {
				if rows == nil {
					rows = []row{}
				}
				return printJSON(map[string]any{"environments": rows, "active": p.ActiveEnv()})
			}
			if len(rows) == 0 {
				info("No environments yet. Create one with `merapi env add dev --base-url http://localhost:8080`.")
				return nil
			}
			table([]string{"", "NAME", "BASE URL", "VARS", "SECRETS"}, tableRows)
			return nil
		},
	}
}

func envShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show an environment: base URL, variables, secret names (values stay hidden)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := envs.Resolve(p, flagEnv)
			if len(args) == 1 {
				name = args[0]
			}
			env, err := envs.Load(p, name)
			if err != nil {
				return err
			}
			var secretNames []string
			if secrets, err := envs.LoadSecrets(p, name); err == nil {
				for k := range secrets {
					secretNames = append(secretNames, k)
				}
				sort.Strings(secretNames)
			} else {
				warn("%v", err)
			}
			if flagJSON {
				return printJSON(map[string]any{
					"name":        env.Name,
					"baseUrl":     env.BaseURL,
					"variables":   env.Variables,
					"secretNames": orEmptyStrings(secretNames),
					"active":      p.ActiveEnv() == name,
				})
			}
			info("Environment: %s%s", env.Name, activeMarker(p.ActiveEnv() == name))
			info("Base URL:    %s", env.BaseURL)
			if len(env.Variables) > 0 {
				info("Variables:")
				keys := make([]string, 0, len(env.Variables))
				for k := range env.Variables {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				rows := make([][]string, 0, len(keys))
				for _, k := range keys {
					rows = append(rows, []string{k, env.Variables[k]})
				}
				table([]string{"KEY", "VALUE"}, rows)
			} else {
				info("Variables:   (none — set with `merapi var set KEY value`)")
			}
			if len(secretNames) > 0 {
				info("Secrets:     %s", fmt.Sprintf("%v", secretNames))
				info("             (encrypted with %s; reference in tests as {{secret:NAME}})", envs.SecretsKeyEnvVar)
			} else {
				info("Secrets:     (none — set with `merapi secret set KEY`)")
			}
			return nil
		},
	}
}

func envAddCmd() *cobra.Command {
	var baseURL string
	var force bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			env, err := envs.Create(p, args[0], baseURL, force)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "name": env.Name, "baseUrl": env.BaseURL})
			}
			success("Environment %s created (base URL: %s)", env.Name, env.BaseURL)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", "http://localhost:8080", "base URL for requests, e.g. https://api.example.com")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing environment")
	return cmd
}

func envUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Select the active environment for test runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			if _, err := envs.Load(p, args[0]); err != nil {
				return err
			}
			if err := p.SetActiveEnv(args[0]); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "active": args[0]})
			}
			success("Active environment is now %s", args[0])
			return nil
		},
	}
}

func envDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an environment and its encrypted secrets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			if err := envs.Delete(p, args[0]); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true})
			}
			success("Environment %s deleted", args[0])
			return nil
		},
	}
}

func activeMarker(active bool) string {
	if active {
		return paint(ansiGreen, " (active)")
	}
	return ""
}

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
