package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/teststore"
)

func registrationServer(t *testing.T, mode string) (context.Context, *httptest.Server, *camp.RegistrationService) {
	t.Helper()
	ctx := context.Background()
	store := camp.NewStore(teststore.New())
	state := camp.NewMemState()
	auth := camp.NewAuth(state)
	auth.Identities = state
	if _, err := store.CreateSpace(ctx, camp.Space{ID: "workspace", Kind: "team", Owner: "system"}); err != nil {
		t.Fatal(err)
	}
	reg := &camp.RegistrationService{Auth: auth, Identities: state, Store: store, Policy: camp.RegistrationPolicy{Mode: mode, StarterWorkspace: "workspace"}}
	sessions := camp.NewSessions(camp.NewBroker(store, auth), executor{})
	server := httptest.NewServer(New(sessions, reg))
	t.Cleanup(server.Close)
	return ctx, server, reg
}

func post(t *testing.T, url string, body string, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAPIRegistrationDisabledFailsClosed(t *testing.T) {
	_, server, _ := registrationServer(t, "disabled")
	resp := post(t, server.URL+"/v1/agents/register", `{"name":"codex"}`, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}

func TestAPIOpenRegistrationAndIdentity(t *testing.T) {
	ctx, server, reg := registrationServer(t, "open")
	resp := post(t, server.URL+"/v1/agents/register", `{"name":"codex-laptop","client":"codex"}`, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var reg1 camp.Registration
	if err := json.NewDecoder(resp.Body).Decode(&reg1); err != nil {
		t.Fatal(err)
	}
	if reg1.Token == "" || reg1.AgentID == "" || reg1.UserID == "" || reg1.Workspace != "workspace" {
		t.Fatalf("registration shape: %+v", reg1)
	}

	// Identity endpoint verifies the saved credential reconnects.
	req, _ := http.NewRequest("GET", server.URL+"/v1/identity", nil)
	req.Header.Set("Authorization", "Bearer "+reg1.Token)
	idResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer idResp.Body.Close()
	if idResp.StatusCode != http.StatusOK {
		t.Fatalf("identity: %d", idResp.StatusCode)
	}
	var identity map[string]any
	if err = json.NewDecoder(idResp.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identity["user_id"] != reg1.UserID {
		t.Fatalf("identity user mismatch: %+v", identity)
	}

	// Same display name from another caller: distinct identity, never the
	// original token.
	resp2 := post(t, server.URL+"/v1/agents/register", `{"name":"codex-laptop"}`, "")
	defer resp2.Body.Close()
	var reg2 camp.Registration
	if err = json.NewDecoder(resp2.Body).Decode(&reg2); err != nil {
		t.Fatal(err)
	}
	if reg2.AgentID == reg1.AgentID || reg2.Token == reg1.Token {
		t.Fatal("name reuse recovered the original identity")
	}

	// Registering again under the first credential attaches to that user.
	resp3 := post(t, server.URL+"/v1/agents/register", `{"name":"codex-desktop"}`, reg1.Token)
	defer resp3.Body.Close()
	var reg3 camp.Registration
	if err = json.NewDecoder(resp3.Body).Decode(&reg3); err != nil {
		t.Fatal(err)
	}
	if reg3.UserID != reg1.UserID || reg3.AgentID == reg1.AgentID {
		t.Fatalf("second agent for same user: %+v", reg3)
	}
	_ = ctx
	_ = reg
}

func TestAPIInvalidBearerNeverFallsBackToOpen(t *testing.T) {
	_, server, _ := registrationServer(t, "open")
	resp := post(t, server.URL+"/v1/agents/register", `{"name":"codex"}`, "not-a-real-token")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid credential must fail closed, got %d", resp.StatusCode)
	}
}

func TestAPIRegistrationRequestCannotCarryGrants(t *testing.T) {
	_, server, _ := registrationServer(t, "open")
	resp := post(t, server.URL+"/v1/agents/register", `{"name":"x","role":"admin"}`, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown fields must be rejected, got %d", resp.StatusCode)
	}
}

func TestAPISessionResumeDeniedAfterOwnerDisabled(t *testing.T) {
	ctx, server, reg := registrationServer(t, "open")
	resp := post(t, server.URL+"/v1/agents/register", `{"name":"codex"}`, "")
	defer resp.Body.Close()
	var registration camp.Registration
	if err := json.NewDecoder(resp.Body).Decode(&registration); err != nil {
		t.Fatal(err)
	}

	// Start a session with the fresh credential.
	start := post(t, server.URL+"/v1/sessions/start", `{"primary_space":"workspace"}`, registration.Token)
	defer start.Body.Close()
	if start.StatusCode != http.StatusOK {
		t.Fatalf("start: %d", start.StatusCode)
	}
	var started camp.SessionView
	if err := json.NewDecoder(start.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	// Disable the owning user: both a resumed run and a fresh registration
	// are denied.
	user, err := reg.Identities.User(ctx, registration.UserID)
	if err != nil {
		t.Fatal(err)
	}
	user.Disabled = true
	if err = reg.Identities.SaveUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	resume := post(t, server.URL+"/v1/sessions/"+started.ID+"/run", `{"operation_id":"op1","code":"x"}`, registration.Token)
	defer resume.Body.Close()
	if resume.StatusCode != http.StatusUnauthorized && resume.StatusCode != http.StatusForbidden {
		t.Fatalf("resumed run after owner disable: %d", resume.StatusCode)
	}
	again := post(t, server.URL+"/v1/agents/register", `{"name":"again"}`, registration.Token)
	defer again.Body.Close()
	if again.StatusCode != http.StatusForbidden {
		t.Fatalf("re-registration after owner disable: %d", again.StatusCode)
	}
	_ = time.Now
}
