package main

import (
	"net/http"
	"os"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher/cline"
	"github.com/spf13/cobra"
)

// mcpCommand is the hidden `prizmal mcp search`, the MCP stdio server a Cline
// launch registers as its prizmal_search tool. Cline runs it; a person never
// needs to.
func mcpCommand() *cobra.Command {
	mcp := &cobra.Command{
		Use:    "mcp",
		Short:  "MCP servers a harness launch runs",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	mcp.AddCommand(&cobra.Command{
		Use:           "search",
		Short:         "Serve web search through the Prizmal Switch over MCP stdio",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client := &http.Client{Timeout: cline.SearchTimeout}
			return cline.ServeSearch(cmd.Context(), os.Stdin, os.Stdout, os.Getenv, client)
		},
	})
	return mcp
}
