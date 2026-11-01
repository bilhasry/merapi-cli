package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bilhasry/merapi/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the Model Context Protocol server (for AI agents) on stdio",
		Long: `Run merapi as an MCP server over stdio. Any MCP-capable AI agent (Claude,
Cursor, custom agents...) can then manage this project through typed tools:
spec_summary, spec_validate, endpoint_add/update/delete, schema_*,
test_create/scaffold/run, env_*, var_*, secret_* (values never exposed).

Claude Code example:
  claude mcp add merapi -- merapi mcp

Cursor / generic MCP client config example:
  { "mcpServers": { "merapi": { "command": "merapi", "args": ["mcp"] } } }

The server resolves the project from the working directory (or --project).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := mustProject()
			if err != nil {
				return fmt.Errorf("%w\nThe MCP server must run inside an merapi project (run `merapi init` first)", err)
			}
			// Keep stdout clean: only MCP frames may go there.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return mcpserver.Run(ctx, p.Root, Version)
		},
	}
}
