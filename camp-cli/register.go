package campcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denisqq/agent-camp/installer/profiles"
	"github.com/denisqq/agent-camp/installer/setup"
	"github.com/denisqq/agent-camp/internal/credstore"
	"github.com/spf13/cobra"
)

type agentRegistration struct {
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	Token     string `json:"token"`
	Workspace string `json:"workspace"`
}

// registerOnServer performs one registration HTTP call against an existing
// Camp server. Shared by camp register and camp setup.
func registerOnServer(ctx context.Context, server, name, clientLabel, token string) (agentRegistration, error) {
	var out agentRegistration
	body, _ := json.Marshal(map[string]string{"name": name, "client": clientLabel})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(server, "/")+"/v1/agents/register", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return out, fmt.Errorf("camp server at %s is unreachable: %w", server, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("registration failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if out.Token == "" {
		return out, fmt.Errorf("server returned no credential; registration incomplete — rerun")
	}
	return out, nil
}

// httpRegisterer adapts registerOnServer to internal/setup.Registerer.
type httpRegisterer struct{}

func (httpRegisterer) Register(ctx context.Context, server, name, clientLabel, existingToken string) (setup.Registration, error) {
	reg, err := registerOnServer(ctx, server, name, clientLabel, existingToken)
	if err != nil {
		return setup.Registration{}, err
	}
	return setup.Registration{UserID: reg.UserID, AgentID: reg.AgentID, Token: reg.Token, Workspace: reg.Workspace}, nil
}

func (httpRegisterer) Verify(ctx context.Context, server, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(server, "/")+"/v1/identity", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity check returned %d", resp.StatusCode)
	}
	return nil
}

// registerCmd registers an agent for an established user (or, on an
// explicitly open server, a new local user). The returned identity and
// credential are saved privately; a display name alone never recovers an
// existing identity.
func registerCmd() *cobra.Command {
	var serverAddr, name, clientLabel, profileName, token, asProfile string
	c := &cobra.Command{
		Use:   "register",
		Short: "Register an agent on a Camp server and save the credential privately",
		RunE: func(cmd *cobra.Command, args []string) error {
			if serverAddr == "" || name == "" {
				return fmt.Errorf("--server and --name are required")
			}
			if profileName == "" {
				profileName = profileFromServer(serverAddr)
			}
			return registerRun(cmd.Context(), serverAddr, name, clientLabel, profileName, token, asProfile)
		},
	}
	c.Flags().StringVar(&serverAddr, "server", "", "camp server URL (required)")
	c.Flags().StringVar(&name, "name", "", "agent display name (required)")
	c.Flags().StringVar(&clientLabel, "client", "", "optional client label, e.g. codex")
	c.Flags().StringVar(&profileName, "profile", "", "profile name to save (default: derived from the server)")
	c.Flags().StringVar(&token, "token", "", "existing credential to register under (default: private profile credential)")
	c.Flags().StringVar(&asProfile, "as", "", "register under the user of this saved profile (uses its private credential)")
	return c
}

func registerRun(ctx context.Context, server, name, clientLabel, profileName, token, asProfile string) error {
	var err error
	server, err = setup.NormalizeServer(server)
	if err != nil {
		return err
	}
	profileStore := profiles.Store{Dir: profiles.DefaultDir()}
	creds := credstore.Store{Dir: credstore.DefaultDir()}
	if token == "" {
		for _, candidate := range []string{asProfile, profileName} {
			if candidate == "" {
				continue
			}
			if profile, err := profileStore.Load(candidate); err == nil && profile.CredentialRef != "" {
				endpoint, err := setup.NormalizeServer(profile.ServerURL)
				if err != nil || endpoint != server {
					return fmt.Errorf("profile %q belongs to a different server; choose a different profile name", candidate)
				}
				if saved, err := creds.Load(profile.CredentialRef); err == nil {
					token = saved
					break
				}
			}
		}
	}
	registration, err := registerOnServer(ctx, server, name, clientLabel, token)
	if err != nil {
		return err
	}
	launcher, err := absoluteLauncher()
	if err != nil {
		return err
	}
	ref := "agent-" + registration.AgentID
	if err := creds.Save(ref, registration.Token); err != nil {
		return err
	}
	profile := profiles.Profile{Name: profileName, ServerURL: server, UserID: registration.UserID, AgentID: registration.AgentID, CredentialRef: ref, Launcher: launcher, Workspace: registration.Workspace}
	if err := profileStore.Save(profile); err != nil {
		return err
	}
	fmt.Printf("Registered agent %s (user %s) on %s.\nSaved private profile %q with workspace %q.\nConnect clients with: camp connect <client> --profile %s\n",
		registration.AgentID, registration.UserID, server, profileName, registration.Workspace, profileName)
	return nil
}

func profileFromServer(server string) string {
	u := strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://")
	u = strings.SplitN(u, "/", 2)[0]
	u = strings.ReplaceAll(strings.ReplaceAll(u, ":", "-"), ".", "-")
	if u == "" {
		return "default"
	}
	return u
}
