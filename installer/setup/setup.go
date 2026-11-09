// Package setup implements the idempotent Camp onboarding flow: prepare
// configuration and data, establish the local user and starter workspace,
// register an agent, save its credential privately, and install selected
// clients. Rerunning reuses identities, workspaces, and choices; a failure
// at any stage resumes safely on the next run.
package setup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/denisqq/agent-camp/installer/profiles"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/credstore"
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
	Verify(ctx context.Context, server, token string) error
}

// ClientInstaller installs one client integration; production uses
// clientconfig.Install, tests inject a fake that can fail on demand.
type ClientInstaller interface {
	Install(client, profile string) error
}

// RuntimePaths are the absolute Python/adapter/Monty locations a local
// server should use (from a runtime pack or a development checkout).
type RuntimePaths struct {
	Python, Script, Monty string
}

// RuntimeAcquirer supplies the matched server runtime for local setup.
// Production downloads a verified pack; tests inject a fake.
type RuntimeAcquirer interface {
	Acquire(ctx context.Context, version string) (RuntimePaths, error)
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
	// Local marks a local-instance run (setup prepares the runtime and
	// the instance bootstrap); remote runs skip both.
	Local    bool
	Launcher string

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
	Workspace     string `json:"workspace,omitempty"`
	// DoneClients are client keys already integrated in an earlier run.
	DoneClients []string `json:"done_clients"`
}

// AcquireRuntime obtains the matched server runtime and persists its
// paths into the configuration, so the server finds a working runner on
// its first start. Callers run this before bootstrapping or starting the
// local service. A failed acquisition aborts setup: an instance whose
// runner cannot start is not a working installation. A nil acquirer is a
// no-op (development checkouts supply their own runner).
func AcquireRuntime(ctx context.Context, acquirer RuntimeAcquirer, version, configPath string) error {
	if acquirer == nil {
		return nil
	}
	paths, err := acquirer.Acquire(ctx, version)
	if err != nil {
		return fmt.Errorf("acquire server runtime: %w (previous installation, if any, is untouched)", err)
	}
	cfg, err := config.Load(configPath, config.Overrides{})
	if err != nil {
		return err
	}
	cfg.RunnerPython, cfg.RunnerScript, cfg.RunnerMonty = paths.Python, paths.Script, paths.Monty
	return config.Save(configPath, cfg)
}

// Run performs setup. It is idempotent: existing identities and client
// entries are reused, never duplicated. Concurrent runs are serialized by
// a setup lock in private profile storage, so parallel invocations cannot
// register duplicate identities.
func Run(ctx context.Context, o Options) (Result, error) {
	if o.ConfigPath == "" {
		o.ConfigPath = config.UserPath()
	}
	profileStore := profiles.Store{Dir: profiles.DefaultDir()}
	release, err := acquireSetupLock(profileStore.Dir)
	if err != nil {
		return Result{}, err
	}
	defer release()
	return run(ctx, o, profileStore)
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

func run(ctx context.Context, o Options, profileStore profiles.Store) (Result, error) {
	if o.AgentName == "" {
		o.AgentName = defaultAgentName()
	}

	server := o.Server
	if server == "" {
		cfg, err := config.Load(o.ConfigPath, config.Overrides{})
		if err != nil {
			return Result{}, err
		}
		server = "http://" + cfg.Listen
	}
	server, err := NormalizeServer(server)
	if err != nil {
		return Result{}, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(server)))[:16]
	statePath := filepath.Join(profileStore.Dir, "setup-"+key+".json")
	var st state
	if body, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(body, &st); err != nil {
			return Result{}, fmt.Errorf("invalid setup state: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	// Migrate the previous single-server setup record only for its own endpoint.
	if st.Server == "" {
		if body, err := os.ReadFile(filepath.Join(profileStore.Dir, "setup-state.json")); err == nil {
			var old state
			if json.Unmarshal(body, &old) == nil {
				if endpoint, err := NormalizeServer(old.Server); err == nil && endpoint == server {
					st = old
				}
			}
		}
	}
	// Existing named registration profiles can also establish this connection.
	if st.AgentID == "" {
		names, err := profileStore.List()
		if err != nil {
			return Result{}, err
		}
		for _, name := range names {
			p, err := profileStore.Load(name)
			if err != nil {
				continue
			}
			endpoint, err := NormalizeServer(p.ServerURL)
			if err == nil && endpoint == server && p.AgentID != "" {
				st = state{Server: server, UserID: p.UserID, AgentID: p.AgentID, CredentialRef: p.CredentialRef, Workspace: p.Workspace}
				break
			}
		}
	}
	st.Server = server

	// Stage 2: identity. Reuse the saved agent credential; for a local
	// instance fall back to the privately stored administrator credential,
	// which provisions agents under the instance owner. No token or role
	// prompt exists on this path.
	token := ""
	if st.CredentialRef != "" {
		if saved, err := (credstore.Store{Dir: credstore.DefaultDir()}).Load(st.CredentialRef); err == nil {
			token = saved
		}
	}
	if token == "" && st.AgentID == "" && o.Local {
		if admin, err := (credstore.Store{Dir: credstore.DefaultDir()}).Load("server-admin"); err == nil {
			token = admin
		}
	}

	if st.AgentID != "" {
		verifyErr := fmt.Errorf("credential unavailable")
		if token != "" {
			verifyErr = o.Registerer.Verify(ctx, st.Server, token)
		}
		if verifyErr != nil {
			// An explicit camp register may have replaced the old credential. Reuse
			// only a verified profile for this exact endpoint; never register silently.
			recovered := false
			names, err := profileStore.List()
			if err != nil {
				return Result{}, err
			}
			for _, name := range names {
				p, err := profileStore.Load(name)
				if err != nil || p.CredentialRef == st.CredentialRef {
					continue
				}
				endpoint, err := NormalizeServer(p.ServerURL)
				if err != nil || endpoint != server || p.AgentID == "" {
					continue
				}
				candidate, err := (credstore.Store{Dir: credstore.DefaultDir()}).Load(p.CredentialRef)
				if err != nil {
					continue
				}
				if err := o.Registerer.Verify(ctx, server, candidate); err != nil {
					continue
				}
				st = state{Server: server, UserID: p.UserID, AgentID: p.AgentID, CredentialRef: p.CredentialRef, Workspace: p.Workspace}
				token = candidate
				recovered = true
				break
			}
			if !recovered {
				return Result{}, fmt.Errorf("saved agent credential rejected or unavailable: %w (use camp register to reconnect)", verifyErr)
			}
		}
	}

	if st.AgentID == "" {
		reg, err := o.Registerer.Register(ctx, st.Server, o.AgentName, "", token)
		if err != nil {
			return Result{}, fmt.Errorf("register agent: %w", err)
		}
		if reg.Token == "" || reg.AgentID == "" || reg.UserID == "" {
			return Result{}, fmt.Errorf("server returned an incomplete registration")
		}
		st.UserID, st.AgentID, st.Workspace = reg.UserID, reg.AgentID, reg.Workspace
		st.CredentialRef = "agent-" + reg.AgentID
		if err := (credstore.Store{Dir: credstore.DefaultDir()}).Save(st.CredentialRef, reg.Token); err != nil {
			// Credential not saved: report incomplete rather than a lost
			// token; the next run registers fresh (the identity record
			// stays but its token was never delivered to a client).
			return Result{}, fmt.Errorf("save credential privately: %w (rerun camp setup)", err)
		}
	}

	profile := profiles.Profile{
		Name:          "local",
		Launcher:      o.Launcher,
		Workspace:     st.Workspace,
		ServerURL:     st.Server,
		UserID:        st.UserID,
		AgentID:       st.AgentID,
		CredentialRef: st.CredentialRef,
	}
	if err := profileStore.Save(profile); err != nil {
		return Result{}, err
	}

	// Stage 3: clients. Failures are resumable: completed clients are
	// recorded, the next run continues with the rest.
	result := Result{Server: st.Server, UserID: st.UserID, AgentID: st.AgentID, Workspace: st.Workspace}
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

// NormalizeServer preserves endpoint boundaries while ignoring a trailing slash.
func NormalizeServer(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid Camp server URL")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
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
	return agentNameForHost(host)
}

// Hostnames can exceed the registration API's 64-byte display-name limit.
func agentNameForHost(host string) string {
	if len(host) > 64 {
		host = host[:64]
	}
	return host
}
