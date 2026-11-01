package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage encrypted secrets (AES-256-GCM, never committed in plaintext)",
		Long: `Manage secrets for environments. Values are encrypted at rest with a key
derived (scrypt) from the ` + envs.SecretsKeyEnvVar + ` environment variable and stored in
.merapi/secrets.<env>.enc (gitignored). In tests, reference them as {{secret:NAME}}.

Set the passphrase before managing secrets, e.g.:
  export MERAPI_SECRETS_KEY="$(openssl rand -hex 32)"`,
	}
	cmd.AddCommand(secretListCmd())
	cmd.AddCommand(secretSetCmd())
	cmd.AddCommand(secretGetCmd())
	cmd.AddCommand(secretUnsetCmd())
	return cmd
}

func secretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secret names of the active environment (values are never shown here)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := envs.Resolve(p, flagEnv)
			secrets, err := envs.LoadSecrets(p, name)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(secrets))
			for k := range secrets {
				names = append(names, k)
			}
			sort.Strings(names)
			if flagJSON {
				return printJSON(map[string]any{"environment": name, "secretNames": names})
			}
			if len(names) == 0 {
				info("No secrets in environment %s. Add one with `merapi secret set KEY` (value prompted, never stored in shell history).", name)
				return nil
			}
			info("Secrets in environment %s (values encrypted with %s):", name, envs.SecretsKeyEnvVar)
			for _, n := range names {
				info("  - %s", n)
			}
			return nil
		},
	}
}

func secretSetCmd() *cobra.Command {
	var valueFile string
	cmd := &cobra.Command{
		Use:   "set <key> [value]",
		Short: "Set a secret (prompts securely when the value is omitted)",
		Long: `Set a secret value. When the value is omitted, merapi prompts with hidden
input (nothing is echoed, nothing lands in shell history) — this is the
recommended way. Alternatively pipe the value: echo -n "$TOKEN" | merapi secret set API_TOKEN`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			var value string
			switch {
			case valueFile != "":
				data, err := os.ReadFile(valueFile)
				if err != nil {
					return err
				}
				value = strings.TrimRight(string(data), "\n")
			case len(args) == 2:
				value = args[1]
				warn("passing secrets as arguments may leak into shell history; prefer omitting the value to get a hidden prompt")
			default:
				value, err = promptSecret("Value: ")
				if err != nil {
					return err
				}
			}
			if value == "" {
				return fmt.Errorf("refusing to store an empty secret")
			}
			name := envs.Resolve(p, flagEnv)
			if err := envs.SetSecret(p, name, args[0], value); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "environment": name, "key": args[0]})
			}
			success("Secret %s stored encrypted in environment %s", args[0], name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&valueFile, "value-file", "F", "", "read the secret value from a file")
	return cmd
}

func secretGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print a secret value (explicit opt-in; prefer {{secret:KEY}} in tests)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := envs.Resolve(p, flagEnv)
			secrets, err := envs.LoadSecrets(p, name)
			if err != nil {
				return err
			}
			v, ok := secrets[args[0]]
			if !ok {
				return fmt.Errorf("secret %q not set in environment %s", args[0], name)
			}
			fmt.Println(v)
			return nil
		},
	}
}

func secretUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return err
			}
			name := envs.Resolve(p, flagEnv)
			if err := envs.UnsetSecret(p, name, args[0]); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true})
			}
			success("Secret %s removed from environment %s", args[0], name)
			return nil
		},
	}
}

// promptSecret reads a line from the terminal without echoing it.
func promptSecret(label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	fmt.Fprint(os.Stderr, label)
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		if err == syscall.EINTR {
			return "", fmt.Errorf("interrupted")
		}
		return "", err
	}
	return string(raw), nil
}
