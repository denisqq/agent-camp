package main

import (
	"github.com/datretyakov/agent-camp/internal/api"
	"github.com/datretyakov/agent-camp/internal/camp"
	"github.com/datretyakov/agent-camp/internal/control"
	"github.com/datretyakov/agent-camp/internal/filestore"
	"github.com/datretyakov/agent-camp/internal/index"
	"github.com/datretyakov/agent-camp/internal/objectstore"
	"github.com/datretyakov/agent-camp/internal/runner"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
func main() {
	if err := run(); err != nil {
		slog.Error("camp stopped", "error", err)
		os.Exit(1)
	}
}

// openStore selects the storage mode: a local directory by default, or S3 for
// shared deployments. Both speak the same object contract and key layout, so
// a camp can move between them by copying data.
func openStore(ctx context.Context) (objectstore.Store, error) {
	if env("CAMP_STORE", "file") == "s3" {
		return objectstore.NewS3(ctx, objectstore.S3Config{
			Endpoint:  os.Getenv("CAMP_S3_ENDPOINT"),
			Region:    env("AWS_REGION", "us-east-1"),
			Bucket:    env("CAMP_S3_BUCKET", "agent-camp"),
			AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
			SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		})
	}
	return filestore.Open(env("CAMP_ROOT", "./camp-data/objects"))
}
func root() string { return env("CAMP_ROOT", "./camp-data") }

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: camp init|serve|token|index|backup|restore")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	objects, err := openStore(ctx)
	if err != nil {
		return err
	}
	store := camp.NewStore(objects)
	// The control plane (credentials, sessions, receipts) is operational
	// state: its own SQLite file, never part of workspace memory.
	ctl, err := control.Open(env("CAMP_CONTROL_PATH", filepath.Join(root(), "control.sqlite")))
	if err != nil {
		return err
	}
	defer ctl.Close()
	auth := camp.NewAuth(ctl)
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
		if err = auth.Bootstrap(ctx, adminToken); err != nil {
			return err
		}
		if _, err = store.CreateSpace(ctx, camp.Space{ID: "shared", Kind: "shared", Owner: "system"}); err != nil && !errors.Is(err, camp.ErrConflict) {
			return err
		}
		fmt.Printf("Camp initialized at %s\nAdmin token: %s\nStore it now: it is not saved anywhere else.\n", root(), adminToken)
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
	case "backup":
		return store.Backup(ctx, os.Stdout)
	case "restore":
		return store.Restore(ctx, os.Stdin)
	case "serve", "index":
	default:
		return errors.New("unknown command")
	}
	indexPath := env("CAMP_INDEX_PATH", filepath.Join(root(), "index.sqlite"))
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
	pool := runner.New(runner.Config{Python: env("CAMP_RUNNER_PYTHON", "./runner/.venv/bin/python"), Script: env("CAMP_RUNNER_SCRIPT", "./runner/camp_runner.py"), Monty: env("CAMP_MONTY_BINARY", "./runner/.venv/bin/monty"), Workers: envInt("CAMP_RUNNERS", 2)})
	defer pool.Close()
	broker := camp.NewBroker(store, auth)
	broker.Search = idx
	sessions := camp.NewSessions(broker, pool)
	sessions.State = ctl
	handler := api.New(sessions)
	server := &http.Server{Addr: env("CAMP_LISTEN", "127.0.0.1:8090"), Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
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
	slog.Info("camp listening", "address", server.Addr, "store", env("CAMP_STORE", "file"))
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
