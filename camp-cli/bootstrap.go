package campcli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/denisqq/agent-camp/camp-cli/guide"
	"github.com/spf13/cobra"
)

func bootstrapCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use: "bootstrap", Short: "Print offline Camp startup instructions for an agent client",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return writeBootstrap(cmd.OutOrStdout(), format) },
	}
	cmd.Flags().StringVar(&format, "format", "claude-code", "output format (claude-code or cursor)")
	return cmd
}

func writeBootstrap(w io.Writer, format string) error {
	switch format {
	case "claude-code":
		return json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "SessionStart", "additionalContext": guide.Bootstrap}})
	case "cursor":
		return json.NewEncoder(w).Encode(map[string]string{"additional_context": guide.Bootstrap})
	default:
		return fmt.Errorf("unsupported bootstrap format %q", format)
	}
}
