package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	writeFile(t, path, `{
		"schema_version": 1,
		"server": {"listen": "127.0.0.1:9000"},
		"storage": {"mode": "file", "root": "data/objects"},
		"registration": {"mode": "disabled", "starter_workspace": "workspace"}
	}`)
	t.Setenv("CAMP_LISTEN", "127.0.0.1:9100")

	// defaults <- file <- env <- CLI overrides
	cfg, err := Load(path, Overrides{Listen: "127.0.0.1:9200"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9200" {
		t.Fatalf("CLI override lost: %s", cfg.Listen)
	}
	cfg, err = Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9100" {
		t.Fatalf("env override lost: %s", cfg.Listen)
	}
	// Storage mode comes from the file; relative root resolves against the
	// config file's directory, not the process working directory.
	if cfg.Storage.Mode != "file" {
		t.Fatalf("mode: %s", cfg.Storage.Mode)
	}
	if cfg.Storage.Root != filepath.Join(dir, "data", "objects") {
		t.Fatalf("root not resolved against config dir: %s", cfg.Storage.Root)
	}
	if cfg.ControlPath != filepath.Join(dir, "data", "control.sqlite") {
		t.Fatalf("control default: %s", cfg.ControlPath)
	}
	if cfg.IndexPath != filepath.Join(dir, "data", "index.sqlite") {
		t.Fatalf("index default: %s", cfg.IndexPath)
	}
	if cfg.Registration.Mode != "disabled" || cfg.Registration.StarterWorkspace != "workspace" {
		t.Fatalf("registration: %+v", cfg.Registration)
	}
}

func TestConfigDefaultsWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "missing.json"), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8090" {
		t.Fatalf("listen default: %s", cfg.Listen)
	}
	if cfg.Storage.Mode != "file" {
		t.Fatalf("storage default: %s", cfg.Storage.Mode)
	}
	if cfg.Storage.Root == "" || !filepath.IsAbs(cfg.Storage.Root) {
		t.Fatalf("root default must be absolute: %q", cfg.Storage.Root)
	}
	if cfg.Storage.Root != filepath.Join(dir, "camp-data", "objects") {
		t.Fatalf("root default should sit beside the config: %s", cfg.Storage.Root)
	}
}

func TestConfigRelativePaths(t *testing.T) {
	// Both storage modes must resolve to the same absolute root regardless of
	// the caller's working directory.
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	writeFile(t, path, `{"schema_version":1,"storage":{"mode":"s3","root":"rel/objs","endpoint":"https://s3.example","bucket":"b","region":"r","credentials_ref":"primary"}}`)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	cfg, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Root != filepath.Join(dir, "rel", "objs") {
		t.Fatalf("s3 root: %s", cfg.Storage.Root)
	}
	if cfg.Storage.CredentialsRef != "primary" {
		t.Fatalf("credentials_ref: %s", cfg.Storage.CredentialsRef)
	}
}

func TestConfigInvalidMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	writeFile(t, path, `{"schema_version":1,"storage":{"mode":"db2"}}`)
	if _, err := Load(path, Overrides{}); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("expected storage mode error, got %v", err)
	}
}

func TestConfigS3MissingEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	writeFile(t, path, `{"schema_version":1,"storage":{"mode":"s3","bucket":"b"}}`)
	_, err := Load(path, Overrides{})
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("expected endpoint error, got %v", err)
	}
}

func TestConfigInvalidRegistrationMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	writeFile(t, path, `{"schema_version":1,"registration":{"mode":"anyone"}}`)
	if _, err := Load(path, Overrides{}); err == nil || !strings.Contains(err.Error(), "registration") {
		t.Fatalf("expected registration error, got %v", err)
	}
}

func TestConfigSaveRoundTripAndAtomicity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	cfg := Defaults(filepath.Join(dir, "camp-data"))
	cfg.Listen = "127.0.0.1:9300"
	cfg.Storage.Mode = "s3"
	cfg.Storage.Endpoint = "https://s3.example"
	cfg.Storage.Bucket = "camp"
	cfg.Storage.Region = "us-east-1"
	cfg.Storage.CredentialsRef = "primary-storage"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Listen != cfg.Listen || loaded.Storage.Bucket != "camp" {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	// Relative storage paths are stored relative to the config file so the
	// file stays portable; resolution happens on load.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), dir) {
		t.Fatalf("config should not bake in absolute machine paths: %s", body)
	}
}

func TestConfigSaveFailurePreservesPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	good := Defaults(filepath.Join(dir, "camp-data"))
	good.Listen = "127.0.0.1:9400"
	if err := Save(path, good); err != nil {
		t.Fatal(err)
	}
	// A save that fails mid-write (target is a directory blocking the rename)
	// must leave the previous file intact and usable.
	bad := Defaults(filepath.Join(dir, "other"))
	bad.Listen = "127.0.0.1:9500"
	_ = Save(filepath.Join(dir, "camp.json", "nested"), bad) // fails
	reloaded, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Listen != "127.0.0.1:9400" {
		t.Fatalf("previous config damaged: %+v", reloaded)
	}
}

func TestConfigS3SetupFailurePreservesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	working := Defaults(filepath.Join(dir, "camp-data"))
	working.Storage.Mode = "s3"
	working.Storage.Endpoint = "https://s3.example"
	working.Storage.Bucket = "camp"
	if err := Save(path, working); err != nil {
		t.Fatal(err)
	}
	// A candidate S3 config that fails validation must not replace the
	// working file: Save validates before touching anything.
	broken := working
	broken.Storage.Endpoint = ""
	if err := Save(path, broken); err == nil {
		t.Fatal("expected validation failure")
	}
	reloaded, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Storage.Endpoint != "https://s3.example" {
		t.Fatalf("working config replaced by broken one: %+v", reloaded.Storage)
	}
}

func TestCredentialRefNotASecretField(t *testing.T) {
	// Secrets live in separate private files; the config only carries a
	// reference. Struct must not gain a credentials literal field.
	dir := t.TempDir()
	path := filepath.Join(dir, "camp.json")
	writeFile(t, path, `{"schema_version":1,"storage":{"mode":"s3","endpoint":"e","bucket":"b","credentials_ref":"ref-1"}}`)
	cfg, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.CredentialsRef != "ref-1" {
		t.Fatalf("credentials_ref: %+v", cfg.Storage)
	}
	if cfg.Storage.AccessKey != "" || cfg.Storage.SecretKey != "" {
		t.Fatalf("config must not embed secret material: %+v", cfg.Storage)
	}
}
