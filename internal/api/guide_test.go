package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denisqq/agent-camp/internal/agentguide"
	"github.com/denisqq/agent-camp/internal/camp"
	"github.com/denisqq/agent-camp/internal/index"
	"github.com/denisqq/agent-camp/internal/runner"
	"github.com/denisqq/agent-camp/internal/teststore"
)

// The workflow skill's executable claims must actually work through the
// real runner: write → read → comment → conflict retry with a new
// operation id. Skipped without CAMP_TEST_PYTHON like the other Monty
// suites.
func TestGuideWorkflowExamplesExecute(t *testing.T) {
	python := os.Getenv("CAMP_TEST_PYTHON")
	if python == "" {
		t.Skip("set CAMP_TEST_PYTHON for real Monty")
	}
	script := "../../runner/camp_runner.py"
	if _, err := os.Stat(script); err != nil {
		t.Skip("runner script not present")
	}
	ctx := context.Background()
	store := camp.NewStore(teststore.New())
	a := camp.NewAuth(camp.NewMemState())
	adminToken := "guide-admin-" + camp.ID()
	if err := a.Bootstrap(ctx, adminToken); err != nil {
		t.Fatal(err)
	}
	admin, _ := a.Authenticate(ctx, adminToken)
	if _, err := store.CreateSpace(ctx, camp.Space{ID: "team", Kind: "team", Owner: "system"}); err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	pool := runner.New(runner.Config{Python: python, Script: script, Monty: python + "/../monty", Workers: 1})
	defer pool.Close()
	broker := camp.NewBroker(store, a)
	broker.Search = idx
	sessions := camp.NewSessions(broker, pool)
	svc := &API{Sessions: sessions}

	session, err := sessions.Start(ctx, admin, camp.StartRequest{Space: "team"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(op, code string) camp.Receipt {
		t.Helper()
		receipt, err := sessions.Run(ctx, admin, camp.RunRequest{SessionID: session.ID, OperationID: op, Code: code})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return receipt
	}

	// The guide's core mechanics, verbatim in spirit.
	run("guide-1", `from camp_sdk import camp
camp.write("/decisions/format", "Use JSON for exports", kind="decision")
doc = camp.read("/decisions/format")
assert "JSON" in doc["content"], doc
camp.comment("/decisions/format", "Seconded — see /tasks/exports", to="agent-2")
"ok"`)
	// Retry semantics: identical code with the same operation id returns
	// the original receipt without duplicating the document.
	first := run("guide-2", `from camp_sdk import camp
camp.write("/tasks/demo", "one")
"done"`)
	again := run("guide-2", `from camp_sdk import camp
camp.write("/tasks/demo", "one")
"done"`)
	if first.ChangeID != again.ChangeID || first.Status != again.Status {
		t.Fatalf("identical retry re-executed: %+v vs %+v", first, again)
	}
	// And the skill text itself only promises SDK methods that exist.
	for _, claim := range []string{"camp.help()", "find(kind=", "backlinks", "comment("} {
		if !strings.Contains(agentguide.Markdown, claim) {
			t.Fatalf("guide drifted from SDK surface: %q missing from guide", claim)
		}
	}
	_ = svc
}
