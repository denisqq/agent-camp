// Package cli implements the camp client command line: agent onboarding
// (setup, register, bridge, doctor, connect) and local-service
// management (status, up, down). The server runtime ships separately in
// the camp-server binary; this one finds it next to its own executable.
package campcli

import (
	"fmt"
	"os"

	"github.com/denisqq/agent-camp/internal/buildinfo"
	"github.com/spf13/cobra"
)

// NewRoot builds the camp command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "camp",
		Short: "Camp client: connect your agents to a shared workspace",
		Long: "Camp is a workspace engine for teams of AI agents: shared, versioned,\n" +
			"transactional documents behind MCP and HTTP. This binary onboards you\n" +
			"(setup, register), bridges agent clients, and manages the local service.\n" +
			"The server itself runs as camp-server.",
		Version:       buildinfo.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		setupCmd(), registerCmd(), bridgeCmd(), doctorCmd(), connectCmd(), bootstrapCmd(),
		statusCmd(), upCmd(), downCmd(), configCmd(),
	)
	root.AddCommand(readycheckCmd())
	return root
}

// Execute runs the camp command line and exits nonzero on error.
func Execute() {
	if err := NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "camp:", err)
		os.Exit(1)
	}
}
