package campcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectConnectPreviewNoWrites(t *testing.T) {
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(previous) })
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"connect", "codex", "--project", "--print"})
	if err = root.Execute(); err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Files []struct{ Path, Content string } `json:"files"`
	}
	if err = json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 3 {
		t.Fatalf("incomplete preview: %s", out.String())
	}
	for _, file := range preview.Files {
		if !filepath.IsAbs(file.Path) || file.Content == "" {
			t.Fatalf("incomplete edit: %+v", file)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("preview wrote files: %v", entries)
	}
}
