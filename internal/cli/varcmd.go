package cli

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/spf13/cobra"
)

func newVarCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "var",
		Short: "Manage plain environment variables (non-secret, committed to git)",
	}
	cmd.AddCommand(varListCmd())
	cmd.AddCommand(varGetCmd())
	cmd.AddCommand(varSetCmd())
	cmd.AddCommand(varUnsetCmd())
	return cmd
}

func varListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the variables of the active environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := envs.Resolve(p, flagEnv)
			env, err := envs.Load(p, name)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"environment": env.Name, "baseUrl": env.BaseURL, "variables": env.Variables})
			}
			if len(env.Variables) == 0 {
				info("No variables in environment %s. Set one with `merapi var set KEY value`.", env.Name)
				return nil
			}
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
			return nil
		},
	}
}

func varGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one variable value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := envs.Resolve(p, flagEnv)
			env, err := envs.Load(p, name)
			if err != nil {
				return err
			}
			v, ok := env.Variables[args[0]]
			if !ok {
				return fmt.Errorf("variable %q not set in environment %s", args[0], name)
			}
			fmt.Println(v)
			return nil
		},
	}
}

func varSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> [value]",
		Short: "Set a variable (reads stdin when the value is omitted)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			value := ""
			if len(args) == 2 {
				value = args[1]
			} else {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				value = string(data)
			}
			name := envs.Resolve(p, flagEnv)
			if err := envs.SetVariable(p, name, args[0], value); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "environment": name, "key": args[0]})
			}
			success("Set %s in environment %s", args[0], name)
			return nil
		},
	}
}

func varUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a variable",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := envs.Resolve(p, flagEnv)
			if err := envs.UnsetVariable(p, name, args[0]); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true})
			}
			success("Removed %s from environment %s", args[0], name)
			return nil
		},
	}
}
