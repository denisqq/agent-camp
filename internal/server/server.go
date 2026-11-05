// Package server owns the server-side runtime: opening the engine
// (store, control plane, auth, index), bootstrapping an instance, and
// running the HTTP or stdio MCP server. The CLI in internal/cli wires
// user commands to these entry points; nothing here parses flags.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/denisqq/agent-camp/internal/api"
	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/control"
	"github.com/denisqq/agent-camp/internal/filestore"
	"github.com/denisqq/agent-camp/internal/index"
	"github.com/denisqq/agent-camp/internal/objectstore"
	"github.com/denisqq/agent-camp/internal/runner"
)

// Engine bundles one open server instance.
type Engine struct {
	Store *camp.Store
	Auth  *camp.Auth
	Ctl   *control.Store
	Index *index.Index
	cfg   config.Config
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

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

// OpenStore selects the storage mode: a local directory by default, or S3
// for shared deployments. Both speak the same object contract and key
// layout, so a camp can move between them by copying data.
func OpenStore(ctx context.Context, cfg config.Config) (objectstore.Store, error) {
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

// Open brings up the full engine. Callers must Close.
func Open(ctx context.Context, cfg config.Config) (*Engine, error) {
	objects, err := OpenStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	store := camp.NewStore(objects)
	// The control plane (credentials, sessions, receipts) is operational
	// state: its own SQLite file, never part of workspace memory.
	ctl, err := control.Open(cfg.ControlPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.IndexPath), 0o700); err != nil {
		ctl.Close()
		return nil, err
	}
	idx, err := index.Open(cfg.IndexPath)
	if err != nil {
		ctl.Close()
		return nil, err
	}
	auth := camp.NewAuth(ctl)
	auth.Identities = ctl
	return &Engine{Store: store, Auth: auth, Ctl: ctl, Index: idx, cfg: cfg}, nil
}

// Close releases the engine's resources.
func (e *Engine) Close() {
	if e.Index != nil {
		e.Index.Close()
	}
	if e.Ctl != nil {
		e.Ctl.Close()
	}
}

// Bootstrap initializes an instance: admin credential plus the reserved
// shared space (and, for fresh setups, the starter workspace).
func (e *Engine) Bootstrap(ctx context.Context, adminToken string, starterWorkspace bool) error {
	if objects, err := OpenStore(ctx, e.cfg); err == nil {
		if s3, ok := objects.(*objectstore.S3); ok {
			if err := s3.EnsureBucket(ctx); err != nil {
				return err
			}
		}
	}
	if err := e.Auth.Bootstrap(ctx, adminToken); err != nil {
		return err
	}
	spaces := []camp.Space{{ID: "shared", Kind: "shared", Owner: "system"}}
	if starterWorkspace {
		spaces = append(spaces, camp.Space{ID: e.cfg.Registration.StarterWorkspace, Kind: "team", Owner: "system"})
	}
	for _, space := range spaces {
		if _, err := e.Store.CreateSpace(ctx, space); err != nil && !errors.Is(err, camp.ErrConflict) {
			return err
		}
	}
	return nil
}

// SyncIndex refreshes the search projection for every space.
func (e *Engine) SyncIndex(ctx context.Context) error {
	spaces, err := e.Store.Spaces(ctx)
	if err != nil {
		return err
	}
	for _, space := range spaces {
		if space.Kind == "system" {
			continue
		}
		if err = e.Index.Sync(ctx, e.Store, space.ID); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) sessions(pool *runner.Pool) *camp.Sessions {
	broker := camp.NewBroker(e.Store, e.Auth)
	broker.Search = e.Index
	sessions := camp.NewSessions(broker, pool)
	sessions.State = e.Ctl
	return sessions
}

// Serve runs the foreground HTTP server until ctx is canceled.
func (e *Engine) Serve(ctx context.Context) error {
	pool := runner.New(runner.Config{Python: e.cfg.RunnerPython, Script: e.cfg.RunnerScript, Monty: e.cfg.RunnerMonty, Workers: envInt("CAMP_RUNNERS", e.cfg.Runner)})
	defer pool.Close()
	sessions := e.sessions(pool)
	registrations := &camp.RegistrationService{Auth: e.Auth, Identities: e.Ctl, Store: e.Store, Policy: camp.RegistrationPolicy{Mode: e.cfg.Registration.Mode, StarterWorkspace: e.cfg.Registration.StarterWorkspace}}
	handler := api.New(sessions, registrations)
	server := &http.Server{Addr: e.cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	// Accepted commits push index syncs; the periodic pass is only a
	// backstop for commits made outside this process (init, restore, CLI).
	e.Store.OnCommit = func(space string) {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := e.Index.Sync(syncCtx, e.Store, space); err != nil {
				slog.Error("index sync failed", "space", space, "error", err)
			}
		}()
	}
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			if err := e.SyncIndex(ctx); err != nil && ctx.Err() == nil {
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
	slog.Info("camp listening", "address", server.Addr, "store", e.cfg.Storage.Mode)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stdio runs the MCP server over standard io for clients that spawn the
// server as a process, authenticating CAMP_TOKEN at startup.
func (e *Engine) Stdio(ctx context.Context) error {
	pool := runner.New(runner.Config{Python: e.cfg.RunnerPython, Script: e.cfg.RunnerScript, Monty: e.cfg.RunnerMonty, Workers: envInt("CAMP_RUNNERS", e.cfg.Runner)})
	defer pool.Close()
	return api.NewAPI(e.sessions(pool)).RunStdio(ctx)
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
