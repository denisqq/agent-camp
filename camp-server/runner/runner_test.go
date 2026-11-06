package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealMontySDKAndLimits(t *testing.T) {
	python := os.Getenv("CAMP_TEST_PYTHON")
	if python == "" {
		t.Skip("CAMP_TEST_PYTHON required for real Monty")
	}
	script, err := filepath.Abs("../../runner/camp_runner.py")
	if err != nil {
		t.Fatal(err)
	}
	pool := New(Config{Python: python, Script: script, Monty: filepath.Join(filepath.Dir(python), "monty"), Workers: 1})
	defer pool.Close()
	ctx := context.Background()
	calls := 0
	cb := func(ctx context.Context, method string, args map[string]any) (any, error) {
		calls++
		return map[string]any{"content": "привет"}, nil
	}
	result, err := pool.Execute(ctx, "from camp_sdk import camp\ncamp.read('/a')['content']", cb)
	if err != nil || result.Value != "привет" || calls != 1 {
		t.Fatalf("SDK: %+v %v calls=%d", result, err, calls)
	}
	for _, code := range []string{"open('/etc/passwd').read()", "import socket\nsocket.socket()", "from camp_sdk import camp\ncamp._callback", "'x'*1000000000", "print('x'*20000)"} {
		if _, err = pool.Execute(ctx, code, cb); err == nil {
			t.Fatalf("accepted unsafe/oversized code: %s", code)
		}
	}
	if _, err = pool.Execute(ctx, "private_value=42\nNone", cb); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Execute(ctx, "private_value", cb); err == nil {
		t.Fatal("globals leaked")
	}
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err = pool.Execute(deadline, "while True:\n    pass", cb); err == nil {
		t.Fatal("infinite loop survived deadline")
	}
	result, err = pool.Execute(ctx, "6*7", cb)
	if err != nil || result.Value != float64(42) {
		t.Fatalf("pool did not recover: %+v %v", result, err)
	}
}
