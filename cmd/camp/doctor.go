package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/denisqq/agent-camp/internal/clientconfig"
)

// camp doctor reports the installation's health: binary and profile
// status, server reachability, and credential access. Its checks are
// read-only unless --write is passed, in which case it creates an
// identified test document through the server (with normal revision
// history) and reports the cleanup path. Secrets are never printed.
func doctor(args []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	profileName := flags.String("profile", "", "profile to check (default: all saved profiles)")
	write := flags.Bool("write", false, "also perform a real write/read check through the server")
	jsonOut := flags.Bool("json", false, "machine-readable output for agent-driven setup")
	if err := flags.Parse(args); err != nil {
		return err
	}

	type report struct {
		Profile string `json:"profile"`
		Server  string `json:"server"`
		AgentID string `json:"agent_id,omitempty"`
		State   string `json:"state"` // reachable+authorized | reachable+unauthorized | unreachable | configured
		Detail  string `json:"detail,omitempty"`
	}
	var reports []report
	store := clientconfig.Store{Dir: clientconfig.DefaultStoreDir()}
	entries, _ := os.ReadDir(store.Dir)
	checked := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "profile-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		profile := strings.TrimSuffix(strings.TrimPrefix(name, "profile-"), ".json")
		if *profileName != "" && profile != *profileName {
			continue
		}
		if checked[profile] {
			continue
		}
		checked[profile] = true
		p, err := store.LoadProfile(profile)
		if err != nil {
			reports = append(reports, report{Profile: profile, State: "configured", Detail: "profile unreadable: " + err.Error()})
			continue
		}
		r := report{Profile: p.Name, Server: p.ServerURL, AgentID: p.AgentID}
		token, credErr := store.LoadCredential(p.CredentialRef)
		switch {
		case credErr != nil:
			r.State, r.Detail = "configured", "credential unavailable — registration needs attention"
		default:
			status, detail := checkServer(p.ServerURL, token)
			r.State, r.Detail = status, detail
			if status == "reachable+authorized" && *write {
				if wErr := writeCheck(p.ServerURL, token); wErr != nil {
					r.Detail += "; write check failed: " + wErr.Error()
				} else {
					r.Detail += "; write/read check passed (test document removed; its revision history remains, like any deletion)"
				}
			}
		}
		reports = append(reports, r)
	}
	if len(reports) == 0 {
		reports = append(reports, report{Profile: *profileName, State: "configured", Detail: "no saved profiles; run camp register or camp setup"})
	}

	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(reports)
	}
	for _, r := range reports {
		fmt.Printf("profile %s → %s\n  server: %s\n  state: %s\n", r.Profile, r.Server, r.Server, r.State)
		if r.Detail != "" {
			fmt.Printf("  detail: %s\n", r.Detail)
		}
	}
	return nil
}

func checkServer(server, token string) (string, string) {
	req, _ := http.NewRequest(http.MethodGet, strings.TrimSuffix(server, "/")+"/v1/identity", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "unreachable", "server did not answer: " + err.Error()
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return "reachable+authorized", ""
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "reachable+unauthorized", "credential rejected — registration needs attention; no admin fallback exists"
	default:
		return "unreachable", fmt.Sprintf("health: %d", resp.StatusCode)
	}
}

// writeCheck performs a real document write and read through the server
// under /doctor/installation-check, then deletes the document. The deletion
// keeps normal revision history; doctor reports that openly.
func writeCheck(server, token string) error {
	base := strings.TrimSuffix(server, "/")
	client := &http.Client{Timeout: 60 * time.Second}
	do := func(method, path string, body io.Reader) ([]byte, int, error) {
		req, err := http.NewRequest(method, base+path, body)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return raw, resp.StatusCode, nil
	}

	startBody := `{"primary_space":"workspace"}`
	raw, status, err := do(http.MethodPost, "/v1/sessions/start", strings.NewReader(startBody))
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("start: %d %s", status, raw)
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.ID == "" {
		return fmt.Errorf("start response missing session id")
	}
	code := "from camp_sdk import camp\n" +
		`camp.write("/doctor/installation-check", "camp doctor write/read verification")` + "\n" +
		`doc = camp.read("/doctor/installation-check")` + "\n" +
		`assert "verification" in doc["content"], doc` + "\n" +
		`camp.delete("/doctor/installation-check")` + "\n" +
		`"doctor: passed"` + "\n"
	payload := fmt.Sprintf(`{"operation_id":"doctor-check","code":%q}`, code)
	raw, status, err = do(http.MethodPost, "/v1/sessions/"+started.ID+"/run", strings.NewReader(payload))
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("run: %d %s", status, raw)
	}
	return nil
}
