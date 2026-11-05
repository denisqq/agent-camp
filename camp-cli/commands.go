package campcli

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/denisqq/agent-camp/internal/config"
	"github.com/spf13/cobra"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// serverBinary locates camp-server next to the running camp executable,
// so GUI-launched clients and setup work without a shell PATH.
func serverBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	candidate := filepath.Join(filepath.Dir(exe), "camp-server")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Fall back to PATH for system installs that split the binaries.
	if found, err := exec.LookPath("camp-server"); err == nil {
		return found, nil
	}
	return candidate, nil
}

// absoluteLauncher returns the absolute path of this camp binary so
// client configs work without a shell PATH.
func absoluteLauncher() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration (no secrets)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(config.Overrides{})
			if err != nil {
				return err
			}
			return printConfig(cfg)
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the local service state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return statusCommand(nil)
		},
	}
}

func upCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Start the local camp-server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return upCommand(nil)
		},
	}
}

func downCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the local camp-server (data preserved)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return downCommand(nil)
		},
	}
}

// readycheckCmd probes a readiness URL; used by container healthchecks.
func readycheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "readycheck <url>",
		Short:  "Exit 0 when the URL answers 200 (healthcheck helper)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(args[0])
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("not ready: %d", resp.StatusCode)
			}
			return nil
		},
	}
}
