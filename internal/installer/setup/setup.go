// Package setup implements the idempotent Camp onboarding flow: prepare
// configuration and data, establish the local user and starter workspace,
// register an agent, save its credential privately, and install selected
// clients. Rerunning reuses identities, workspaces, and choices; a failure
// at any stage resumes safely on the next run.
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/denisqq/agent-camp/internal/clientconfig"
	"github.com/denisqq/agent-camp/internal/config"
)

// Registration is what a register operation returns (local or remote).
type Registration struct {
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	Token     string `json:"token"`
	Workspace string `json:"workspace"`
}

// Registerer registers an agent on a server; production uses the HTTP
// endpoint, tests inject a fake.
type Registerer interface {
	Register(ctx context.Context, server, name, clientLabel, existingToken string) (Registration, error)
}

// ClientInstaller installs one client integration; production uses
// clientconfig.Install, tests inject a fake that can fail on demand.
type ClientInstaller interface {
	Install(client, profile string) error
}

// Options configure one setup run.
type Options struct {
	// ConfigPath is the persistent configuration file (empty: user default).
	ConfigPath string
	// Server is an existing remote server; empty means local instance.
	Server string
	// AgentName is the display name for this machine's agent.
	AgentName string
	// Clients lists client keys to integrate (claude-code, codex, ...).
	Clients []string
	// Registerer and Installer are injectable seams.
	Registerer      Registerer
	ClientInstaller ClientInstaller
}

// Result summarizes the run.
type Result struct {
	Server    string   `json:"server"`
	Workspace string   `json:"workspace"`
	UserID    string   `json:"user_id"`
	AgentID   string   `json:"agent_id"`
	Clients   []string `json:"clients"`
	Pending   []string `json:"pending,omitempty"`
}

// state is the resumable setup record kept in private profile storage.
type state struct {
	Server        string `json:"server"`
	UserID        string `json:"user_id"`
	AgentID       string `json:"agent_id"`
	CredentialRef string `json:"credential_ref"`
	// DoneClients are client keys already integrated in an earlier run.
	DoneClients []string `json:"done_clients"`
}

// Run performs setup. It is idempotent: existing identities and client
// entries are reused, never duplicated. Concurrent runs are serialized by
// a setup lock in private profile storage, so parallel invocations cannot
// register duplicate identities.
func Run(ctx context.Context, o Options) (Result, error) {
	if o.ConfigPath == "" {
		o.ConfigPath = config.UserPath()
	}
	profiles := clientconfig.Store{Dir: clientconfig.DefaultStoreDir()}
	release, err := acquireSetupLock(profiles.Dir)
	if err != nil {
		return Result{}, err
	}
	defer release()
	return run(ctx, o, profiles)
}

func acquireSetupLock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "setup.lock")
	// A stale lock (crashed holder) is reclaimed after a grace period.
	for attempt := 0; ; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 5*time.Minute {
			os.Remove(path) // stale
			continue
		}
		if attempt > 100 {
			return nil, fmt.Errorf("another camp setup is running (lock %s)", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func run(ctx context.Context, o Options, profiles clientconfig.Store) (Result, error) {
	if o.AgentName == "" {
		o.AgentName = defaultAgentName()
	}
	statePath := filepath.Join(profiles.Dir, "setup-state.json")

	var st state
	if body, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(body, &st)
	}

	// Stage 1: configuration. A local instance gets a validated default
	// config; connecting to an existing server skips instance creation.
	if o.Server == "" && st.Server == "" {
		if _, err := os.Stat(o.ConfigPath); os.IsNotExist(err) {
			cfg := config.Defaults(filepath.Join(filepath.Dir(o.ConfigPath), "camp-data"))
			cfg.Registration.StarterWorkspace = "workspace"
			if err := config.Save(o.ConfigPath, cfg); err != nil {
				return Result{}, fmt.Errorf("write configuration: %w", err)
			}
		}
		st.Server = "http://127.0.0.1:8090"
	}
	if o.Server != "" {
		st.Server = o.Server
	}

	// Stage 2: identity. Reuse the saved agent credential; for a local
	// instance fall back to the privately stored administrator credential,
	// which provisions agents under the instance owner. No token or role
	// prompt exists on this path.
	token := ""
	if st.CredentialRef != "" {
		if saved, err := profiles.LoadCredential(st.CredentialRef); err == nil {
			token = saved
		}
	}
	if token == "" {
		if admin, err := profiles.LoadCredential("server-admin"); err == nil {
			token = admin
		}
	}
	if st.AgentID == "" || token == "" {
		reg, err := o.Registerer.Register(ctx, st.Server, o.AgentName, "", token)
		if err != nil {
			return Result{}, fmt.Errorf("register agent: %w", err)
		}
		st.UserID, st.AgentID = reg.UserID, reg.AgentID
		st.CredentialRef = "agent-" + reg.AgentID
		if err := profiles.SaveCredential(st.CredentialRef, reg.Token); err != nil {
			// Credential not saved: report incomplete rather than a lost
			// token; the next run registers fresh (the identity record
			// stays but its token was never delivered to a client).
			return Result{}, fmt.Errorf("save credential privately: %w (rerun camp setup)", err)
		}
	}

	profile := clientconfig.Profile{
		Name:          "local",
		ServerURL:     st.Server,
		UserID:        st.UserID,
		AgentID:       st.AgentID,
		CredentialRef: st.CredentialRef,
	}
	if err := profiles.SaveProfile(profile); err != nil {
		return Result{}, err
	}

	// Stage 3: clients. Failures are resumable: completed clients are
	// recorded, the next run continues with the rest.
	result := Result{Server: st.Server, UserID: st.UserID, AgentID: st.AgentID, Workspace: "workspace"}
	for _, client := range o.Clients {
		if containsStr(st.DoneClients, client) {
			result.Clients = append(result.Clients, client)
			continue
		}
		if err := o.ClientInstaller.Install(client, "local"); err != nil {
			result.Pending = append(result.Pending, client)
			// Persist progress so far before failing.
			_ = writeState(statePath, st)
			return result, fmt.Errorf("install client %s: %w (rerun camp setup to resume)", client, err)
		}
		st.DoneClients = append(st.DoneClients, client)
		result.Clients = append(result.Clients, client)
	}
	if err := writeState(statePath, st); err != nil {
		return result, err
	}
	return result, nil
}

func writeState(path string, st state) error {
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func defaultAgentName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "camp-agent"
	}
	return host
}
