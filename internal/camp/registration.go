package camp

import (
	"context"
	"errors"
	"strings"
	"time"
)

// RegistrationPolicy is the server's admission policy for new agents.
// Mode is "disabled" (default) or "open". The server — never the request —
// selects the starter workspace, role, and scopes a registration receives.
type RegistrationPolicy struct {
	Mode             string
	StarterWorkspace string
}

// RegisterRequest is what a caller may ask for: a display name and an
// optional client label. There is deliberately no field for an owner ID,
// role, space, or scopes: a registration cannot choose its own grants.
type RegisterRequest struct {
	DisplayName string `json:"name"`
	Client      string `json:"client,omitempty"`
}

// Registration is the one-time result of registering an agent. Token is
// returned exactly once and is never recoverable by name.
type Registration struct {
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	Agent     Agent  `json:"agent"`
	Token     string `json:"token"`
	Workspace string `json:"workspace"`
}

// RegistrationService registers agents for an established user (local
// setup) or, when the policy is open, for a brand-new Camp-local user.
// The cross-store sequence (identity row → CAS-committed workspace grant →
// credential → activation) is an explicit state machine: an agent stays
// "pending" until its grant is committed, and a pending agent exposes no
// usable credential.
type RegistrationService struct {
	Auth       *Auth
	Identities IdentityStore
	Store      *Store
	Policy     RegistrationPolicy
}

const (
	AgentPending = "pending"
	AgentActive  = "active"
)

// ErrRegistrationDisabled is returned when no established user context is
// supplied and the server policy does not allow open registration.
var ErrRegistrationDisabled = errors.New("agent registration is disabled on this server")

// Register creates an agent owned by the user in owner (when non-nil), or
// a fresh Camp-local user when owner is nil and the policy is open. The
// returned credential is scoped to exactly the policy's starter workspace
// with writer access.
func (s *RegistrationService) Register(ctx context.Context, owner *UserContext, req RegisterRequest) (Registration, error) {
	name := strings.TrimSpace(req.DisplayName)
	if name == "" || len(name) > 64 {
		return Registration{}, ErrInvalid
	}
	req.DisplayName = name
	if len(req.Client) > 64 {
		return Registration{}, ErrInvalid
	}

	var user User
	if owner != nil {
		// An established context is required; the caller never picks an
		// arbitrary owner ID. A disabled user cannot register agents.
		u, err := s.Identities.User(ctx, owner.User.ID)
		if err != nil || u.Disabled {
			return Registration{}, ErrDenied
		}
		user = u
	} else {
		if s.Policy.Mode != "open" || s.Policy.StarterWorkspace == "" {
			return Registration{}, ErrRegistrationDisabled
		}
		user = User{ID: "user-" + ID(), DisplayName: name, CreatedAt: time.Now().UTC()}
		if err := s.Identities.CreateUser(ctx, user); err != nil {
			return Registration{}, err
		}
	}

	// Resume or start: a pending agent for this user with the same display
	// name continues its interrupted registration instead of duplicating.
	agent, err := s.pendingAgent(ctx, user.ID, req.DisplayName)
	if err != nil {
		return Registration{}, err
	}
	if agent.ID == "" {
		agent = Agent{ID: "agent-" + ID(), OwnerUserID: user.ID, DisplayName: req.DisplayName, Client: req.Client, CreatedAt: time.Now().UTC(), Status: AgentPending}
		if err = s.Identities.CreateAgent(ctx, agent); err != nil {
			return Registration{}, err
		}
	}

	// Grant writer access on the starter workspace through the same CAS
	// commit path every space change uses; never publish outside Commit.
	if err = s.grantWorkspace(ctx, agent.ID); err != nil {
		// The agent stays pending and no credential exists yet: the
		// registration is incomplete, not successful.
		return Registration{}, err
	}

	token := ID() + ID()
	principal := Principal{
		Subject:   agent.ID,
		Role:      "writer",
		Spaces:    []string{s.Policy.StarterWorkspace},
		Scopes:    []string{"read", "write", "propose"},
		Expires:   time.Now().AddDate(1, 0, 0),
		TokenHash: digest(token),
		AgentID:   agent.ID,
	}
	if err = s.Auth.Principals.SavePrincipal(ctx, principal); err != nil {
		return Registration{}, err
	}
	agent.Status = AgentActive
	if err = s.Identities.SaveAgent(ctx, agent); err != nil {
		return Registration{}, err
	}
	return Registration{UserID: user.ID, AgentID: agent.ID, Agent: agent, Token: token, Workspace: s.Policy.StarterWorkspace}, nil
}

// pendingAgent finds this user's pending agent with the given display name,
// if an earlier registration was interrupted after the identity row was
// written but before activation.
func (s *RegistrationService) pendingAgent(ctx context.Context, userID, name string) (Agent, error) {
	agents, err := s.Identities.AgentsByOwner(ctx, userID)
	if err != nil {
		return Agent{}, err
	}
	for _, a := range agents {
		if a.Status == AgentPending && a.DisplayName == name {
			return a, nil
		}
	}
	return Agent{}, nil
}

// grantWorkspace merges a writer share entry for the agent into the starter
// workspace's access list through Store.Commit. Merging (not replacing)
// makes retries and concurrent registrations idempotent; a conflicting
// commit from a concurrent registration is retried once.
func (s *RegistrationService) grantWorkspace(ctx context.Context, agentID string) error {
	for attempt := 0; attempt < 2; attempt++ {
		base, err := s.Store.Load(ctx, s.Policy.StarterWorkspace)
		if err != nil {
			return err
		}
		if base.Space.Kind == "shared" {
			// The reserved shared kind has a fixed policy; agent grants
			// target collaborative team spaces only.
			return ErrDenied
		}
		access := base.Space.Access
		if access == nil {
			access = &Access{}
		}
		duplicate := false
		for _, entry := range access.Share {
			if entry.Subject == agentID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			access.Share = append(access.Share, ShareEntry{Subject: agentID, Role: "writer"})
			space := base.Space
			space.Access = access
			if _, err = s.Store.Commit(ctx, base, nil, ID(), Receipt{Status: "completed"}, &space); err != nil {
				if errors.Is(err, ErrConflict) {
					continue
				}
				return err
			}
		}
		return nil
	}
	return ErrConflict
}
