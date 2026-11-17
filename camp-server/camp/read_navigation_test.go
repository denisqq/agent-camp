package camp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestReadContinuationLongUnicodeLine(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("界\\\"", 6000) + "\nend"
	if _, err = b.Call(ctx, e, "write", map[string]any{"path": "/large", "content": content}); err != nil {
		t.Fatal(err)
	}
	got := ""
	line, offset := 1, 0
	for i := 0; i < 50; i++ {
		result, err := b.Call(ctx, e, "read", map[string]any{"path": "/large", "start_line": line, "line_offset": offset})
		if err != nil {
			t.Fatal(err)
		}
		r := result.(map[string]any)
		wire, _ := json.Marshal(RunOutput{Value: r})
		if len(wire) > 14*1024 {
			t.Fatalf("read output too large: %d", len(wire))
		}
		got += r["content"].(string)
		if !r["truncated"].(bool) {
			break
		}
		line = r["next_line"].(int)
		offset = r["next_offset"].(int)
		got += r["next_separator"].(string)
	}
	if got != content {
		t.Fatalf("lost content: got %d want %d", len(got), len(content))
	}
}

func TestReadContinuationLineLimit(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	b.Call(ctx, e, "write", map[string]any{"path": "/short", "content": "a\nb\nc"})
	result, err := b.Call(ctx, e, "read", map[string]any{"path": "/short", "limit": 2})
	if err != nil {
		t.Fatal(err)
	}
	r := result.(map[string]any)
	if r["content"] != "a\nb" || r["next_line"] != 3 || r["next_offset"] != 0 || r["next_separator"] != "\n" || r["truncation_reason"] != "line_limit" {
		t.Fatalf("bad continuation: %#v", r)
	}
}

func TestOutlineSkipsFencedCode(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	b.Call(ctx, e, "write", map[string]any{"path": "/doc", "content": "# Real\n```md\n# Fake\n```\n## Next"})
	result, err := b.Call(ctx, e, "outline", map[string]any{"path": "/doc"})
	if err != nil {
		t.Fatal(err)
	}
	h := result.(map[string]any)["headings"].([]map[string]any)
	if len(h) != 2 || h[0]["line"] != 1 || h[1]["line"] != 5 {
		t.Fatalf("wrong headings: %#v", h)
	}
}

func TestOutlineKeepsHeadingsAfterInlineBackticks(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, err := b.Begin(ctx, p, "alice")
	if err != nil {
		t.Fatal(err)
	}
	content := "# Before\n```inline```\n## After\n```go\n# Hidden\n```\n### Final"
	if _, err = b.Call(ctx, e, "write", map[string]any{"path": "/doc", "content": content}); err != nil {
		t.Fatal(err)
	}
	result, err := b.Call(ctx, e, "outline", map[string]any{"path": "/doc"})
	if err != nil {
		t.Fatal(err)
	}
	outline := result.(map[string]any)
	headings := outline["headings"].([]map[string]any)
	if len(headings) != 3 || headings[0]["line"] != 1 || headings[1]["line"] != 3 || headings[2]["line"] != 7 || outline["truncated"] != false {
		t.Fatalf("wrong outline after inline code: %#v", outline)
	}
}

func TestReadRejectsOffsetInsideUTF8AndBeyondLine(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	b.Call(ctx, e, "write", map[string]any{"path": "/u", "content": "界x\nend"})
	for _, offset := range []int{-1, 1, 2, 5} {
		if _, err := b.Call(ctx, e, "read", map[string]any{"path": "/u", "line_offset": offset}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("offset %d: %v", offset, err)
		}
	}
	result, err := b.Call(ctx, e, "read", map[string]any{"path": "/u", "line_offset": 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["content"] != "x\nend" {
		t.Fatal(result)
	}
}

func TestReadMetadataRespectsRunnerBudgetEvenPastEOF(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	values := make([]any, 6000)
	for i := range values {
		values[i] = 0
	}
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/meta", "content": "text", "metadata": map[string]any{"items": values}}); err != nil {
		t.Fatal(err)
	}
	for _, start := range []int{1, 10} {
		if _, err := b.Call(ctx, e, "read", map[string]any{"path": "/meta", "start_line": start}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("line %d: expected explicit budget error, got %v", start, err)
		}
	}
}

func TestReadFloatMetadataAccountsForPythonEncoding(t *testing.T) {
	ctx, b, p, _ := setupBroker(t)
	e, _ := b.Begin(ctx, p, "alice")
	values := make([]any, 2300)
	for i := range values {
		values[i] = 1e-9
	}
	if _, err := b.Call(ctx, e, "write", map[string]any{"path": "/floats", "content": "x", "metadata": map[string]any{"items": values}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call(ctx, e, "read", map[string]any{"path": "/floats"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected budget error, got %v", err)
	}
}
