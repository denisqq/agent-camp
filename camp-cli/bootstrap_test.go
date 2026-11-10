package campcli

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBootstrapClaudeJSON(t *testing.T) {
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"bootstrap", "--format", "claude-code"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Hook struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hook.Event != "SessionStart" || payload.Hook.Context == "" {
		t.Fatalf("missing startup context: %s", out.String())
	}
}

func TestBootstrapUnknownFormat(t *testing.T) {
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"bootstrap", "--format", "unknown"})
	if err := root.Execute(); err == nil {
		t.Fatal("unknown format accepted")
	}
	if out.Len() != 0 {
		t.Fatal("invalid format emitted output")
	}
}

func TestBootstrapCursorJSON(t *testing.T) {
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"bootstrap", "--format", "cursor"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Context string `json:"additional_context"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Context == "" {
		t.Fatalf("missing Cursor startup context: %s", out.String())
	}
}
