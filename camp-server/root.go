// Package servercmd implements the camp-server command line: the server
// runtime binary (serve, stdio MCP, bootstrap, token admin, backup, and
// server-only setup). It shares internal/server with the client binary
// but ships as its own artifact for deployments that run Camp as a
// service without the onboarding CLI.
package campserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/denisqq/agent-camp/camp-server/camp"
	"github.com/denisqq/agent-camp/camp-server/runtime"
	"github.com/denisqq/agent-camp/internal/buildinfo"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/credstore"
	"github.com/spf13/cobra"
)

// NewRoot builds the camp-server command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "camp-server",
		Short: "Camp server runtime",
		Long: "The Camp workspace server: HTTP /v1 + MCP (map/start/run) over a\n" +
			"versioned, transactional document store. Run it under your process\n" +
			"manager (systemd, Docker, supervisor); it stays in the foreground.",
		Version:       buildinfo.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		serveCmd(), mcpCmd(), initCmd(), initAndServeCmd(),
		tokenCmd(), indexCmd(), backupCmd(), restoreCmd(),
		setupCmd(),
	)
	return root
}

// Execute runs the camp-server command line and exits nonzero on error.
func Execute() {
	if err := NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "camp-server:", err)
		os.Exit(1)
	}
}

func loadCfg() (config.Config, error) {
	return config.Load(configPath(), config.Overrides{})
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := notifyContext(cmd.Context())
			defer cancel()
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(ctx, cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			return engine.Serve(ctx)
		},
	}
}

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve MCP over standard io (CAMP_TOKEN authenticates)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := notifyContext(cmd.Context())
			defer cancel()
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(ctx, cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			return engine.Stdio(ctx)
		},
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize the instance and print an admin token",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(ctx, cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			adminToken := os.Getenv("CAMP_ADMIN_TOKEN")
			if adminToken == "" {
				raw := make([]byte, 32)
				if _, err = rand.Read(raw); err != nil {
					return err
				}
				adminToken = hex.EncodeToString(raw)
			}
			if err = engine.Bootstrap(ctx, adminToken, false); err != nil {
				return err
			}
			fmt.Printf("Camp initialized at %s\nAdmin token: %s\nStore it now: it is not saved anywhere else.\n", cfg.DataRoot, adminToken)
			return nil
		},
	}
}

func initAndServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init-and-serve",
		Short: "Bootstrap with CAMP_ADMIN_TOKEN, then serve",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := notifyContext(cmd.Context())
			defer cancel()
			adminToken := os.Getenv("CAMP_ADMIN_TOKEN")
			if adminToken == "" {
				return errors.New("CAMP_ADMIN_TOKEN is required for init-and-serve")
			}
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(ctx, cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			if err = engine.Bootstrap(ctx, adminToken, false); err != nil {
				return err
			}
			return engine.Serve(ctx)
		},
	}
}

func tokenCmd() *cobra.Command {
	var subject, spaces, role, scopes string
	var days int
	c := &cobra.Command{
		Use:   "token",
		Short: "Issue a token (requires CAMP_ADMIN_TOKEN)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(ctx, cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			admin, err := engine.Auth.Authenticate(ctx, os.Getenv("CAMP_ADMIN_TOKEN"))
			if err != nil {
				return errors.New("set CAMP_ADMIN_TOKEN to the token printed by camp-server init")
			}
			if subject == "" {
				return errors.New("--subject is required")
			}
			token, err := engine.Auth.Issue(ctx, admin, camp.Principal{Subject: subject, Role: role, Spaces: strings.Split(spaces, ","), Scopes: strings.Split(scopes, ","), Expires: time.Now().Add(time.Duration(days) * 24 * time.Hour)})
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"token": token})
		},
	}
	c.Flags().StringVar(&subject, "subject", "", "subject identity, e.g. agent name or user id")
	c.Flags().StringVar(&spaces, "spaces", "shared", "comma-separated spaces")
	c.Flags().StringVar(&role, "role", "author", "role")
	c.Flags().StringVar(&scopes, "scopes", "read,write,propose,delegate", "scopes")
	c.Flags().IntVar(&days, "days", 30, "token lifetime in days")
	return c
}

func indexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Rebuild the search index",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			return engine.SyncIndex(cmd.Context())
		},
	}
}

func backupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Write a workspace backup archive to stdout",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			return engine.Store.Backup(cmd.Context(), os.Stdout)
		},
	}
}

func restoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Restore a workspace backup archive from stdin",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}
			engine, err := server.Open(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			return engine.Store.Restore(cmd.Context(), os.Stdin)
		},
	}
}

// setupCmd is the server-only setup: configuration, control state, the
// starter workspace, and a privately stored administrator credential.
func setupCmd() *cobra.Command {
	var open bool
	c := &cobra.Command{
		Use:   "setup",
		Short: "Prepare a server instance (config, admin credential, starter workspace)",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configPath()
			cfg, err := config.Load(path, config.Overrides{})
			if err != nil {
				return err
			}
			if open {
				cfg.Registration.Mode = "open"
			}
			if err = config.Save(path, cfg); err != nil {
				return err
			}
			if err = os.MkdirAll(cfg.DataRoot, 0o700); err != nil {
				return err
			}
			adminToken := makeToken()
			ctx := cmd.Context()
			engine, err := server.Open(ctx, cfg)
			if err != nil {
				return err
			}
			defer engine.Close()
			if err = engine.Bootstrap(ctx, adminToken, true); err != nil {
				return err
			}
			store := credstore.Store{Dir: credstore.DefaultDir()}
			if err = store.Save("server-admin", adminToken); err != nil {
				return err
			}
			fmt.Printf("Server instance ready at %s (data under %s).\nAdmin credential saved privately (ref server-admin).\nRun: camp-server serve\n", cfg.Listen, cfg.DataRoot)
			return nil
		},
	}
	c.Flags().BoolVar(&open, "open-registration", false, "allow self-registration on this server (explicit owner choice)")
	return c
}

func makeToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}
