// Package cli implements the camp command line: client-side onboarding
// (setup, register, bridge, doctor, connect, status/up/down) plus the
// server-side commands (init, serve, mcp, token, index, backup, restore)
// delegated to internal/server. Flag parsing lives here; the server
// package stays interface-free.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/server"
)

// Run executes the command line and returns the process error.
func Run(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: camp setup|register|bridge|doctor|status|up|down|connect|init|serve|mcp|token|index|backup|restore|config|readycheck")
	}
	switch args[0] {
	// Client-side commands: no engine required.
	case "setup":
		return setupCommand(args[1:])
	case "register":
		return register(args[1:])
	case "bridge":
		return bridge(args[1:])
	case "doctor":
		return doctor(args[1:])
	case "connect":
		return connect(args[1:])
	case "status":
		return statusCommand(args[1:])
	case "up":
		return upCommand(args[1:])
	case "down":
		return downCommand(args[1:])
	case "readycheck":
		return readycheck(args[1:])
	case "config":
		cfg, err := loadConfig(config.Overrides{})
		if err != nil {
			return err
		}
		return printConfig(cfg)
	}

	// Server-side commands: bring up the engine.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg, err := loadConfig(config.Overrides{})
	if err != nil {
		return err
	}
	engine, err := server.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()

	switch args[0] {
	case "init":
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
	case "init-and-serve":
		adminToken := os.Getenv("CAMP_ADMIN_TOKEN")
		if adminToken == "" {
			return errors.New("CAMP_ADMIN_TOKEN is required for init-and-serve")
		}
		if err = engine.Bootstrap(ctx, adminToken, false); err != nil {
			return err
		}
		return engine.Serve(ctx)
	case "serve":
		return engine.Serve(ctx)
	case "mcp":
		return engine.Stdio(ctx)
	case "index":
		return engine.SyncIndex(ctx)
	case "token":
		return tokenCommand(ctx, engine, args[1:])
	case "backup":
		return engine.Store.Backup(ctx, os.Stdout)
	case "restore":
		return engine.Store.Restore(ctx, os.Stdin)
	default:
		return errors.New("unknown command")
	}
}

func tokenCommand(ctx context.Context, engine *server.Engine, args []string) error {
	flags := flag.NewFlagSet("token", flag.ContinueOnError)
	subject := flags.String("subject", "", "subject identity, e.g. agent name or user id")
	spaces := flags.String("spaces", "shared", "comma-separated spaces")
	role := flags.String("role", "author", "role")
	scopes := flags.String("scopes", "read,write,propose,delegate", "scopes")
	days := flags.Int("days", 30, "token lifetime in days")
	if err := flags.Parse(args); err != nil {
		return err
	}
	admin, err := engine.Auth.Authenticate(ctx, os.Getenv("CAMP_ADMIN_TOKEN"))
	if err != nil {
		return errors.New("set CAMP_ADMIN_TOKEN to the token printed by camp init")
	}
	if *subject == "" {
		return errors.New("--subject is required")
	}
	token, err := engine.Auth.Issue(ctx, admin, camp.Principal{Subject: *subject, Role: *role, Spaces: strings.Split(*spaces, ","), Scopes: strings.Split(*scopes, ","), Expires: time.Now().Add(time.Duration(*days) * 24 * time.Hour)})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"token": token})
}

// env returns the environment value or the fallback.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// readycheck probes a readiness URL; used by the container healthcheck.
func readycheck(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: camp readycheck <url>")
	}
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
}
