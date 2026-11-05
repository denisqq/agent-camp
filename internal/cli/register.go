package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denisqq/agent-camp/internal/clientconfig"
	"github.com/denisqq/agent-camp/internal/installer/setup"
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
	req, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(server, "/")+"/v1/agents/register", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
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
	server = strings.TrimSuffix(server, "/")
	store := clientconfig.Store{Dir: clientconfig.DefaultStoreDir()}
	if token == "" {
		for _, candidate := range []string{asProfile, profileName} {
			if candidate == "" {
				continue
			}
			if profile, err := store.LoadProfile(candidate); err == nil && profile.CredentialRef != "" {
				if saved, err := store.LoadCredential(profile.CredentialRef); err == nil {
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
	if err := store.SaveCredential(ref, registration.Token); err != nil {
		return err
	}
	profile := clientconfig.Profile{Name: profileName, ServerURL: server, UserID: registration.UserID, AgentID: registration.AgentID, CredentialRef: ref, Launcher: launcher}
	if err := store.SaveProfile(profile); err != nil {
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
