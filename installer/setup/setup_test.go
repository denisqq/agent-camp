package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/denisqq/agent-camp/installer/profiles"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/credstore"
)

type fakeRegisterer struct {
	mu     sync.Mutex
	tokens int
	users  []string
	agents []string
}

func (f *fakeRegisterer) Register(ctx context.Context, server, name, clientLabel, existingToken string) (Registration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Same established token returns the same identity (reconnect reuse).
	if existingToken != "" {
		for i, u := range f.users {
			_ = u
			_ = i
		}
	}
	f.tokens++
	user := "user-" + strings.Repeat(string(rune('0'+f.tokens)), 1)
	agent := "agent-" + strings.Repeat(string(rune('0'+f.tokens)), 1)
	f.users = append(f.users, user)
	f.agents = append(f.agents, agent)
	return Registration{UserID: user, AgentID: agent, Token: "token-" + agent, Workspace: "workspace"}, nil
}

func (*fakeRegisterer) Verify(context.Context, string, string) error { return nil }

type fakeInstaller struct {
	failFor map[string]bool
	calls   []string
}

func (f *fakeInstaller) Install(client, profile string) error {
	f.calls = append(f.calls, client)
	if f.failFor != nil && f.failFor[client] {
		return errors.New("simulated client failure")
	}
	return nil
}

// isolateHome points private profile storage at a temp directory.
func isolateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("CAMP_CLIENT_CONFIG_DIR", filepath.Join(dir, "private"))
	return dir
}

func TestFreshSetupRequiresNoManualToken(t *testing.T) {
	isolateHome(t)
	reg := &fakeRegisterer{}
	result, err := Run(context.Background(), Options{Local: true, ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: &fakeInstaller{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentID == "" || result.UserID == "" {
		t.Fatalf("identity missing: %+v", result)
	}
	// The credential was saved privately; no manual token step exists.
	profile, err := (profiles.Store{Dir: profiles.DefaultDir()}).Load("local")
	if err != nil {
		t.Fatal(err)
	}
	token, err := (credstore.Store{Dir: credstore.DefaultDir()}).Load(profile.CredentialRef)
	if err != nil || token == "" {
		t.Fatalf("private credential missing: %v", err)
	}
}

func TestRepeatedSetupReusesIDs(t *testing.T) {
	isolateHome(t)
	reg := &fakeRegisterer{}
	opts := Options{Local: true, ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: &fakeInstaller{}}
	first, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentID != second.AgentID || first.UserID != second.UserID {
		t.Fatalf("identities duplicated: %+v vs %+v", first, second)
	}
	if reg.tokens != 1 {
		t.Fatalf("re-registration happened anyway: %d", reg.tokens)
	}
}

func TestSetupResumesAfterClientConfigFailure(t *testing.T) {
	isolateHome(t)
	reg := &fakeRegisterer{}
	installer := &fakeInstaller{failFor: map[string]bool{"codex": true}}
	opts := Options{Local: true, ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: installer, Clients: []string{"claude-code", "codex"}}
	first, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected client failure to surface")
	}
	if len(first.Clients) != 1 || first.Clients[0] != "claude-code" {
		t.Fatalf("completed clients: %+v", first.Clients)
	}

	// Retry with the failure repaired: no duplicate identity, the working
	// client is not reinstalled, the failed one completes.
	installer.failFor = nil
	second, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.AgentID != first.AgentID {
		t.Fatalf("identity changed across resume: %+v", second)
	}
	if len(second.Clients) != 2 {
		t.Fatalf("resume did not complete all clients: %+v", second)
	}
	calls := 0
	for _, c := range installer.calls {
		if c == "claude-code" {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("completed client reinstalled: %v", installer.calls)
	}
}

func TestConcurrentSetupStartsOneServer(t *testing.T) {
	isolateHome(t)
	reg := &fakeRegisterer{}
	opts := Options{Local: true, ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: &fakeInstaller{}}
	var wg sync.WaitGroup
	results := make([]Result, 4)
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = Run(context.Background(), opts)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("setup %d failed: %v", i, err)
		}
	}
	agent := results[0].AgentID
	for i, r := range results {
		if r.AgentID != agent {
			t.Fatalf("setup %d created a duplicate identity", i)
		}
	}
}

type fakeRuntime struct{ fail bool }

func (f fakeRuntime) Acquire(ctx context.Context, version string) (RuntimePaths, error) {
	if f.fail {
		return RuntimePaths{}, errors.New("simulated runtime failure")
	}
	return RuntimePaths{Python: "/r/python", Script: "/r/camp_runner.py", Monty: "/r/monty"}, nil
}

func TestSetupAcquiresRuntimeAndWritesConfig(t *testing.T) {
	isolateHome(t)
	configPath := filepath.Join(t.TempDir(), "camp.json")
	if err := config.Save(configPath, config.Defaults(filepath.Join(t.TempDir(), "camp-data"))); err != nil {
		t.Fatal(err)
	}
	if err := AcquireRuntime(context.Background(), fakeRuntime{}, "1.2.3", configPath); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunnerPython != "/r/python" || cfg.RunnerScript != "/r/camp_runner.py" || cfg.RunnerMonty != "/r/monty" {
		t.Fatalf("runtime paths not persisted: %+v", cfg)
	}
}

func TestSetupRuntimeFailureAbortsCleanly(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "camp.json")
	good := config.Defaults(filepath.Join(dir, "camp-data"))
	if err := config.Save(configPath, good); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := AcquireRuntime(context.Background(), fakeRuntime{fail: true}, "1.2.3", configPath); err == nil || !strings.Contains(err.Error(), "acquire server runtime") {
		t.Fatalf("want runtime abort, got %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed acquisition modified configuration")
	}

}

func TestSetupServerSwitchResetsIdentity(t *testing.T) {
	isolateHome(t)
	configPath := filepath.Join(t.TempDir(), "camp.json")
	reg := &fakeRegisterer{}
	opts := Options{Local: true, Server: "http://127.0.0.1:8090", ConfigPath: configPath, Registerer: reg, ClientInstaller: &fakeInstaller{}}
	first, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	// Different ports may host independent servers: register separately.
	opts.Server = "http://127.0.0.1:9090"
	same, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if same.AgentID == first.AgentID {
		t.Fatalf("loopback port change reused identity")
	}
	// Remote target: fresh registration, never reuse the local credential.
	opts.Server = "https://camp.example.com"
	_, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if reg.tokens != 3 {
		t.Fatalf("server switch did not re-register: %d", reg.tokens)
	}
}

func (*tokenRecorder) Verify(context.Context, string, string) error { return nil }

type tokenRecorder struct{ tokens []string }

func (r *tokenRecorder) Register(_ context.Context, server, name, label, token string) (Registration, error) {
	r.tokens = append(r.tokens, token)
	return Registration{UserID: "u", AgentID: "a", Token: "credential", Workspace: "project"}, nil
}
func TestRemoteSetupNeverUsesLocalAdmin(t *testing.T) {
	isolateHome(t)
	creds := credstore.Store{Dir: credstore.DefaultDir()}
	if err := creds.Save("server-admin", "LOCAL-ADMIN"); err != nil {
		t.Fatal(err)
	}
	r := &tokenRecorder{}
	_, err := Run(context.Background(), Options{Server: "https://remote.example", Registerer: r, ClientInstaller: &fakeInstaller{}})
	if err != nil {
		t.Fatal(err)
	}
	if r.tokens[0] != "" {
		t.Fatal("local administrator credential sent to remote")
	}
}
func TestSetupReturnsToServerIdentity(t *testing.T) {
	isolateHome(t)
	r := &fakeRegisterer{}
	opts := Options{Server: "http://127.0.0.1:8101", Registerer: r, ClientInstaller: &fakeInstaller{}}
	first, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Server = "http://127.0.0.1:8102"
	second, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentID == second.AgentID {
		t.Fatal("different loopback servers share identity")
	}
	opts.Server = "http://127.0.0.1:8101/"
	again, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if again.AgentID != first.AgentID {
		t.Fatal("returning to server lost identity")
	}
}

type revokingRegisterer struct {
	fakeRegisterer
	revoked string
}

func (r *revokingRegisterer) Verify(_ context.Context, server, token string) error {
	if token == r.revoked {
		return errors.New("revoked")
	}
	return nil
}
func TestExplicitRegistrationReplacesRevokedSetupCredential(t *testing.T) {
	isolateHome(t)
	r := &revokingRegisterer{}
	opts := Options{Server: "http://127.0.0.1:8123", Registerer: r, ClientInstaller: &fakeInstaller{}}
	first, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	r.revoked = "token-" + first.AgentID
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("revoked credential accepted")
	}
	creds := credstore.Store{Dir: credstore.DefaultDir()}
	if err := creds.Save("recovered", "new-credential"); err != nil {
		t.Fatal(err)
	}
	if err := (profiles.Store{Dir: profiles.DefaultDir()}).Save(profiles.Profile{Name: "recovered", ServerURL: opts.Server, UserID: first.UserID, AgentID: "replacement", CredentialRef: "recovered"}); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentID != "replacement" {
		t.Fatalf("explicit replacement ignored: %+v", result)
	}
}

func TestLongHostnameFitsRegistrationLimit(t *testing.T) {
	host := strings.Repeat("runner-", 15) + ".local"
	name := agentNameForHost(host)
	if len(name) == 0 || len(name) > 64 {
		t.Fatalf("default registration name has %d bytes; server limit is 64", len(name))
	}
}
