// Command camp is the single entry point; all command logic lives in
// internal/cli (client commands) and internal/server (server runtime).
package main

import (
	"log/slog"
	"os"

	"github.com/denisqq/agent-camp/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		slog.Error("camp stopped", "error", err)
		os.Exit(1)
	}
}
