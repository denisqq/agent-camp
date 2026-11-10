package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denisqq/agent-camp/camp-server/camp"
	"github.com/denisqq/agent-camp/camp-server/teststore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStartSnapshotHandoffHTTPAndMCP(t *testing.T) {
	ctx := t.Context()
	auth := camp.NewAuth(camp.NewMemState())
	const token = "snapshot-handoff-admin-token-long-enough"
	if err := auth.Bootstrap(ctx, token); err != nil {
		t.Fatal(err)
	}
	p, err := auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	store := camp.NewStore(teststore.New())
	if _, err := store.CreateSpace(ctx, camp.Space{ID: "team", Kind: "team", Owner: "system"}); err != nil {
		t.Fatal(err)
	}
	b := camp.NewBroker(store, auth)
	server := httptest.NewServer(New(camp.NewSessions(b, executor{})))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "handoff", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: tokenTransport{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	mapSnapshot := func() string {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "map", Arguments: map[string]any{"space": "team"}})
		if err != nil || result.IsError {
			t.Fatalf("map: %+v %v", result, err)
		}
		raw, _ := json.Marshal(result.StructuredContent)
		var page struct{ Snapshot string }
		if err := json.Unmarshal(raw, &page); err != nil || page.Snapshot == "" {
			t.Fatalf("map snapshot: %s %v", raw, err)
		}
		return page.Snapshot
	}
	for _, transport := range []string{"http", "mcp"} {
		t.Run(transport, func(t *testing.T) {
			start := func(args map[string]any, wantStatus int) camp.SessionView {
				t.Helper()
				var raw []byte
				if transport == "http" {
					body, _ := json.Marshal(args)
					req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/sessions/start", bytes.NewReader(body))
					req.Header.Set("Authorization", "Bearer "+token)
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					if resp.StatusCode != wantStatus {
						t.Fatalf("start HTTP status=%d want=%d", resp.StatusCode, wantStatus)
					}
					var value json.RawMessage
					if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
						t.Fatal(err)
					}
					raw = value
				} else {
					result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "start", Arguments: args})
					if err != nil || result.IsError != (wantStatus != 200) {
						t.Fatalf("start MCP: %+v %v wantStatus=%d", result, err, wantStatus)
					}
					raw, _ = json.Marshal(result.StructuredContent)
				}
				var view camp.SessionView
				if wantStatus == 200 {
					if err := json.Unmarshal(raw, &view); err != nil || view.ID == "" {
						t.Fatalf("start result: %s %v", raw, err)
					}
				}
				return view
			}
			mapped := mapSnapshot()
			matched := start(map[string]any{"primary_space": "team", "expected_snapshot": mapped}, 200)
			if matched.Snapshot != mapped {
				t.Fatalf("handoff changed snapshot: %s want=%s", matched.Snapshot, mapped)
			}
			writer, err := b.Begin(ctx, p, "team")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Call(ctx, writer, "write", map[string]any{"path": "/" + transport, "content": "advanced"}); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Finish(ctx, writer, "advance-"+transport, camp.Receipt{}); err != nil {
				t.Fatal(err)
			}
			start(map[string]any{"primary_space": "team", "expected_snapshot": mapped}, 409)
			fresh := mapSnapshot()
			if fresh == mapped {
				t.Fatal("map did not advance")
			}
			remapped := start(map[string]any{"primary_space": "team", "expected_snapshot": fresh}, 200)
			if remapped.Snapshot != fresh {
				t.Fatal("remapped handoff changed snapshot")
			}
			for _, snapshot := range []string{mapped, fresh} {
				start(map[string]any{"primary_space": "team", "session_id": matched.ID, "expected_snapshot": snapshot}, 400)
			}
			resumed := start(map[string]any{"primary_space": "team", "session_id": matched.ID}, 200)
			if resumed.Snapshot != mapped || resumed.ID != matched.ID {
				t.Fatal("resume changed snapshot")
			}
			unconditional := start(map[string]any{"primary_space": "team"}, 200)
			if unconditional.Snapshot != fresh {
				t.Fatal("legacy start did not choose current snapshot")
			}
		})
	}
}
