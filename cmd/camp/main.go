package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/denisqq/agent-camp/internal/api"
	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/control"
	"github.com/denisqq/agent-camp/internal/filestore"
	"github.com/denisqq/agent-camp/internal/index"
	"github.com/denisqq/agent-camp/internal/objectstore"
	"github.com/denisqq/agent-camp/internal/runner"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envAny returns the first non-empty value among the environment key and the
// given fallbacks.
func envAny(key string, fallbacks ...string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	for _, f := range fallbacks {
		if f != "" {
			return f
		}
	}
	return ""
}
func main() {
	if err := run(); err != nil {
		slog.Error("camp stopped", "error", err)
		os.Exit(1)
	}
}

// openStore selects the storage mode: a local directory by default, or S3 for
// shared deployments. Both speak the same object contract and key layout, so
// a camp can move between them by copying data.
func openStore(ctx context.Context, cfg config.Config) (objectstore.Store, error) {
	if cfg.Storage.Mode == "s3" {
		return objectstore.NewS3(ctx, objectstore.S3Config{
			Endpoint:  cfg.Storage.Endpoint,
			Region:    envAny("AWS_REGION", cfg.Storage.Region, "us-east-1"),
			Bucket:    envAny("CAMP_S3_BUCKET", cfg.Storage.Bucket, "agent-camp"),
			AccessKey: env("AWS_ACCESS_KEY_ID", cfg.Storage.AccessKey),
			SecretKey: env("AWS_SECRET_ACCESS_KEY", cfg.Storage.SecretKey),
		})
	}
	return filestore.Open(cfg.Storage.Root)
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: camp init|serve|mcp|token|connect|index|backup|restore|config")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg, err := loadConfig(config.Overrides{})
	if err != nil {
		return err
	}
	if os.Args[1] == "config" {
		return printConfig(cfg)
	}
	objects, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	store := camp.NewStore(objects)
	// The control plane (credentials, sessions, receipts) is operational
	// state: its own SQLite file, never part of workspace memory.
	ctl, err := control.Open(cfg.ControlPath)
	if err != nil {
		return err
	}
	defer ctl.Close()
	auth := camp.NewAuth(ctl)
	auth.Identities = ctl
	switch os.Args[1] {
	case "init":
		adminToken := os.Getenv("CAMP_ADMIN_TOKEN")
		if adminToken == "" {
			raw := make([]byte, 32)
			if _, err = rand.Read(raw); err != nil {
				return err
			}
			adminToken = hex.EncodeToString(raw)
		}
		if s3, ok := objects.(*objectstore.S3); ok {
			if err = s3.EnsureBucket(ctx); err != nil {
				return err
			}
		}
		if err = auth.Bootstrap(ctx, adminToken); err != nil {
			return err
		}
		if _, err = store.CreateSpace(ctx, camp.Space{ID: "shared", Kind: "shared", Owner: "system"}); err != nil && !errors.Is(err, camp.ErrConflict) {
			return err
		}
		fmt.Printf("Camp initialized at %s\nAdmin token: %s\nStore it now: it is not saved anywhere else.\n", cfg.DataRoot, adminToken)
		return nil
	case "token":
		flags := flag.NewFlagSet("token", flag.ContinueOnError)
		subject := flags.String("subject", "", "subject identity, e.g. agent name or user id")
		spaces := flags.String("spaces", "shared", "comma-separated spaces")
		role := flags.String("role", "author", "role")
		scopes := flags.String("scopes", "read,write,propose,delegate", "scopes")
		days := flags.Int("days", 30, "token lifetime in days")
		if err = flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		admin, err := auth.Authenticate(ctx, os.Getenv("CAMP_ADMIN_TOKEN"))
		if err != nil {
			return errors.New("set CAMP_ADMIN_TOKEN to the token printed by camp init")
		}
		if *subject == "" {
			return errors.New("--subject is required")
		}
		token, err := auth.Issue(ctx, admin, camp.Principal{Subject: *subject, Role: *role, Spaces: strings.Split(*spaces, ","), Scopes: strings.Split(*scopes, ","), Expires: time.Now().Add(time.Duration(*days) * 24 * time.Hour)})
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"token": token})
	case "connect":
		return connect(os.Args[2:])
	case "backup":
		return store.Backup(ctx, os.Stdout)
	case "restore":
		return store.Restore(ctx, os.Stdin)
	case "init-and-serve":
		adminToken := os.Getenv("CAMP_ADMIN_TOKEN")
		if adminToken == "" {
			return errors.New("CAMP_ADMIN_TOKEN is required for init-and-serve")
		}
		if s3, ok := objects.(*objectstore.S3); ok {
			if err = s3.EnsureBucket(ctx); err != nil {
				return err
			}
		}
		if err = auth.Bootstrap(ctx, adminToken); err != nil {
			return err
		}
		if _, err = store.CreateSpace(ctx, camp.Space{ID: "shared", Kind: "shared", Owner: "system"}); err != nil && !errors.Is(err, camp.ErrConflict) {
			return err
		}
		os.Args[1] = "serve"
		fallthrough
	case "serve", "mcp", "index":
	default:
		return errors.New("unknown command")
	}
	indexPath := cfg.IndexPath
	if err = os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return err
	}
	idx, err := index.Open(indexPath)
	if err != nil {
		return err
	}
	defer idx.Close()
	syncIndex := func() error {
		spaces, err := store.Spaces(ctx)
		if err != nil {
			return err
		}
		for _, space := range spaces {
			if space.Kind == "system" {
				continue
			}
			if err = idx.Sync(ctx, store, space.ID); err != nil {
				return err
			}
		}
		return nil
	}
	if os.Args[1] == "index" {
		return syncIndex()
	}
	pool := runner.New(runner.Config{Python: cfg.RunnerPython, Script: cfg.RunnerScript, Monty: cfg.RunnerMonty, Workers: envInt("CAMP_RUNNERS", cfg.Runner)})
	defer pool.Close()
	broker := camp.NewBroker(store, auth)
	broker.Search = idx
	sessions := camp.NewSessions(broker, pool)
	sessions.State = ctl
	registrations := &camp.RegistrationService{Auth: auth, Identities: ctl, Store: store, Policy: camp.RegistrationPolicy{Mode: cfg.Registration.Mode, StarterWorkspace: cfg.Registration.StarterWorkspace}}
	handler := api.New(sessions, registrations)
	if os.Args[1] == "mcp" {
		// Stdio mode for clients that spawn the server as a process.
		return api.NewAPI(sessions).RunStdio(ctx)
	}
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	// Accepted commits push index syncs; the periodic pass is only a backstop
	// for commits made outside this process (init, restore, CLI).
	store.OnCommit = func(space string) {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := idx.Sync(syncCtx, store, space); err != nil {
				slog.Error("index sync failed", "space", space, "error", err)
			}
		}()
	}
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			if err := syncIndex(); err != nil && ctx.Err() == nil {
				slog.Error("index sync failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("camp listening", "address", server.Addr, "store", cfg.Storage.Mode)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		n, err := 0, error(nil)
		fmt.Sscanf(v, "%d", &n)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
