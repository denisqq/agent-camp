package campcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/denisqq/agent-camp/installer/profiles"
	"github.com/denisqq/agent-camp/internal/credstore"
)

func TestSetupInstallsUsableLauncherAndRejectsRevokedCredential(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("CAMP_CLIENT_CONFIG_DIR", filepath.Join(dir, "private"))
	t.Chdir(dir)
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/identity" {
			if revoked {
				w.WriteHeader(403)
			}
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected initial authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"user_id":"user-one","agent_id":"agent-one","token":"agent-secret","workspace":"project"}`))
	}))
	defer server.Close()
	if err := setupRun(context.Background(), server.URL, "test-agent", []string{"cursor"}, false, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".cursor/mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	launcher := doc.Servers["agent-camp"].Command
	if !filepath.IsAbs(launcher) {
		t.Fatalf("MCP launcher is not executable path: %q", launcher)
	}
	if _, err := os.Stat(launcher); err != nil {
		t.Fatal(err)
	}
	revoked = true
	if err := setupRun(context.Background(), server.URL, "test-agent", nil, false, false); err == nil {
		t.Fatal("revoked credential accepted")
	}
}

func TestSetupJSONPreservesFailureExit(t *testing.T) {
	t.Setenv("CAMP_CLIENT_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	if err := setupRun(context.Background(), server.URL, "agent", nil, true, false); err == nil {
		t.Fatal("JSON mode swallowed registration error")
	}
}

func TestRegisterNeverSendsProfileCredentialToDifferentServer(t *testing.T) {
	for _, as := range []bool{false, true} {
		t.Run(fmt.Sprint(as), func(t *testing.T) {
			t.Setenv("CAMP_CLIENT_CONFIG_DIR", t.TempDir())
			creds := credstore.Store{Dir: credstore.DefaultDir()}
			if err := creds.Save("private", "LOCAL-SECRET"); err != nil {
				t.Fatal(err)
			}
			p := profiles.Profile{Name: "existing", ServerURL: "https://original.example", CredentialRef: "private"}
			if err := (profiles.Store{Dir: profiles.DefaultDir()}).Save(p); err != nil {
				t.Fatal(err)
			}
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Header.Get("Authorization") != "" {
					t.Error("credential leaked to another endpoint")
				}
				w.WriteHeader(403)
			}))
			defer server.Close()
			asProfile := ""
			profile := "existing"
			if as {
				asProfile = "existing"
				profile = "new"
			}
			if err := registerRun(context.Background(), server.URL, "agent", "", profile, "", asProfile); err == nil {
				t.Fatal("mismatched profile accepted")
			}
			if called {
				t.Fatal("mismatched profile should fail before network request")
			}
		})
	}
}
