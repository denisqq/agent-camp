package campserver

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/denisqq/agent-camp/internal/config"
)

func configPath() string { return config.DiscoverPath() }

// notifyContext upgrades a context to stop on SIGINT/SIGTERM when the
// caller has not already arranged it.
func notifyContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}
