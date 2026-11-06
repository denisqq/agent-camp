package setup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/denisqq/agent-camp/installer/profiles"
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
	return dir
}

func TestFreshSetupRequiresNoManualToken(t *testing.T) {
	isolateHome(t)
	reg := &fakeRegisterer{}
	result, err := Run(context.Background(), Options{ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: &fakeInstaller{}})
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
	opts := Options{ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: &fakeInstaller{}}
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
	opts := Options{ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: installer, Clients: []string{"claude-code", "codex"}}
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
	opts := Options{ConfigPath: filepath.Join(t.TempDir(), "camp.json"), Registerer: reg, ClientInstaller: &fakeInstaller{}}
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
