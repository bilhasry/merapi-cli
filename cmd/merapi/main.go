// Command merapi is a terminal-first API management tool: OpenAPI 3.1
// documentation, test scenarios, environments with variables and encrypted
// secrets, plus an MCP server so AI agents can operate it.
package main

import (
	"os"

	"github.com/bilhasry/merapi/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
