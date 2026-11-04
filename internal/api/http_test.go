package api

import (
	"bytes"
	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/teststore"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type executor struct{}

func (executor) Execute(ctx context.Context, code string, cb camp.Callback) (camp.RunOutput, error) {
	_, err := cb(ctx, "write", map[string]any{"path": "/goal", "kind": "goal", "content": code})
	return camp.RunOutput{Value: "saved"}, err
}

type tokenTransport struct{ token string }

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}
func TestHTTPAndMCPUseSameIdentityAndStorage(t *testing.T) {
	ctx := context.Background()
	store := camp.NewStore(teststore.New())
	a := camp.NewAuth(camp.NewMemState())
	token := "bootstrap-token-long-enough-for-test"
	if err := a.Bootstrap(ctx, token); err != nil {
		t.Fatal(err)
	}
	store.CreateSpace(ctx, camp.Space{ID: "test", Kind: "personal", Owner: "system"})
	server := httptest.NewServer(New(camp.NewSessions(camp.NewBroker(store, a), executor{})))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/v1/sessions/start", bytes.NewBufferString(`{"primary_space":"test"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("anonymous start accepted", resp.StatusCode)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: tokenTransport{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 3 {
		t.Fatalf("MCP tool surface: %+v %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "start", Arguments: map[string]any{"primary_space": "test"}})
	if err != nil || result.IsError {
		wire, _ := json.Marshal(result)
		t.Fatalf("start: %s %v", wire, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var started camp.SessionView
	if err = json.Unmarshal(encoded, &started); err != nil || started.ID == "" {
		t.Fatalf("start shape: %s %v", encoded, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "run", Arguments: map[string]any{"session_id": started.ID, "operation_id": "first", "code": "growth"}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	snap, _ := store.Load(ctx, "test")
	rec, err := store.RecordByPath(ctx, snap, "/goal")
	if err != nil || rec.Kind != "goal" {
		t.Fatalf("MCP write missing: %+v %v", rec, err)
	}
}

func TestSpaceAdministrationDoesNotExposeRegistryToAuthors(t *testing.T) {
	ctx := context.Background()
	store := camp.NewStore(teststore.New())
	auth := camp.NewAuth(camp.NewMemState())
	token := "bootstrap-token-long-enough-for-test"
	if err := auth.Bootstrap(ctx, token); err != nil {
		t.Fatal(err)
	}
	store.CreateSpace(ctx, camp.Space{ID: "author-one", Kind: "personal", Owner: "one"})
	admin, _ := auth.Authenticate(ctx, token)
	author, err := auth.Issue(ctx, admin, camp.Principal{Subject: "one", Role: "author", Spaces: []string{"author-one"}, Scopes: []string{"read"}, Expires: admin.Expires})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(camp.NewSessions(camp.NewBroker(store, auth), executor{})))
	defer server.Close()
	for _, path := range []string{"/v1/admin/spaces", "/v1/admin/spaces/author-one"} {
		for _, tc := range []struct {
			token  string
			status int
		}{{token, 200}, {author, 403}} {
			req, _ := http.NewRequest("GET", server.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf("%s: got %d want %d", path, res.StatusCode, tc.status)
			}
		}
	}
}

func TestScopedAdminCannotChangeForeignWorkspacePolicy(t *testing.T) {
	ctx := t.Context()
	store := camp.NewStore(teststore.New())
	auth := camp.NewAuth(camp.NewMemState())
	const root = "bootstrap-token-long-enough-for-test"
	if err := auth.Bootstrap(ctx, root); err != nil {
		t.Fatal(err)
	}
	admin, _ := auth.Authenticate(ctx, root)
	snap, err := store.CreateSpace(ctx, camp.Space{ID: "foreign", Kind: "team", Owner: "other"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.Issue(ctx, admin, camp.Principal{Subject: "limited", Role: "admin", Spaces: []string{"own"}, Scopes: []string{"read", "write"}, Expires: admin.Expires})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(camp.NewSessions(camp.NewBroker(store, auth), executor{}))
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/admin/spaces/foreign", ""},
		{"PUT", "/v1/admin/spaces/foreign/access", `{"access":{"visibility":"open"},"expected_commit":"` + snap.Commit + `"}`},
		{"POST", "/v1/admin/spaces", `{"id":"foreign-new","kind":"team","owner":"limited"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s bypassed token ceiling: %d", tc.method, tc.path, response.Code)
		}
	}
	req := httptest.NewRequest("GET", "/v1/admin/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if bytes.Contains(response.Body.Bytes(), []byte("foreign")) {
		t.Fatal("registry leaked foreign workspace")
	}
}

func TestMCPMapWorksBeforeStartAndEnforcesAccess(t *testing.T) {
	ctx := context.Background()
	store := camp.NewStore(teststore.New())
	auth := camp.NewAuth(camp.NewMemState())
	adminToken := "map-admin-token-at-least-32-characters"
	if err := auth.Bootstrap(ctx, adminToken); err != nil {
		t.Fatal(err)
	}
	admin, _ := auth.Authenticate(ctx, adminToken)
	snap, err := store.CreateSpace(ctx, camp.Space{ID: "team", Kind: "team", Owner: "owner", Access: &camp.Access{Share: []camp.ShareEntry{{Subject: "reader", Role: "reader"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Commit(ctx, snap, []camp.Revision{
		{ID: "public-rev", RecordID: "public", Path: "/docs/guide", Kind: "note", Content: "body must not appear", Actor: "owner"},
		{ID: "secret-rev", RecordID: "secret", Path: "/secret", Kind: "section", Content: "hidden", Metadata: map[string]any{"access": map[string]any{"visibility": "closed"}}, Actor: "owner"},
	}, "seed", camp.Receipt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.Issue(ctx, admin, camp.Principal{Subject: "reader", Role: "author", Spaces: []string{"team"}, Scopes: []string{"read"}, Expires: admin.Expires})
	if err != nil {
		t.Fatal(err)
	}
	broker := camp.NewBroker(store, auth)
	// A nil executor proves orientation does not require Monty or a run session.
	server := httptest.NewServer(New(camp.NewSessions(broker, nil)))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "map-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: tokenTransport{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	discovery, discoveryErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "map", Arguments: map[string]any{}})
	if discoveryErr != nil || discovery.IsError {
		t.Fatalf("empty map before session: %+v %v", discovery, discoveryErr)
	}
	discoveryRaw, _ := json.Marshal(discovery.StructuredContent)
	if !bytes.Contains(discoveryRaw, []byte(`"id":"team"`)) {
		t.Fatalf("missing team: %s", discoveryRaw)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "map", Arguments: map[string]any{"space": "team"}})
	if err != nil || result.IsError {
		t.Fatalf("map before start: %+v %v", result, err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data["records"] != float64(1) || data["root"] != "/" || bytes.Contains(raw, []byte("secret")) || bytes.Contains(raw, []byte("body must not appear")) {
		t.Fatalf("map leaked or missed records: %s", raw)
	}
	p, _ := auth.Authenticate(ctx, token)
	e, err := broker.Begin(ctx, p, "team")
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := broker.Call(ctx, e, "map", map[string]any{"path": "/", "depth": 2})
	if err != nil {
		t.Fatal(err)
	}
	sdkRaw, _ := json.Marshal(sdk)
	var sdkData map[string]any
	if err = json.Unmarshal(sdkRaw, &sdkData); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(data, sdkData) {
		t.Fatalf("MCP/SDK differ: %s / %s", raw, sdkRaw)
	}
	for _, args := range []map[string]any{{"space": "other"}, {"space": "team", "depth": 0}, {"space": "team", "depth": 5}, {"space": "team", "path": "/../secret"}} {
		denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "map", Arguments: args})
		if err == nil && !denied.IsError {
			t.Fatalf("invalid map accepted: %+v", args)
		}
	}
}
