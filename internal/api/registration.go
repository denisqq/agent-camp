// Agent registration and identity endpoints. POST /v1/agents/register is
// deliberately outside the bearer-token group: with an established user
// credential it registers under that user, and on an explicitly open
// server it creates a new Camp-local user for an unauthenticated caller.
package api

import (
	"net/http"
	"strings"

	"github.com/denisqq/agent-camp/internal/camp"
)

// register handles POST /v1/agents/register.
func (a *API) register(w http.ResponseWriter, r *http.Request) {
	if a.Registrations == nil {
		reply(w, nil, camp.ErrNotFound)
		return
	}
	var req camp.RegisterRequest
	if err := decode(w, r, &req); err != nil {
		reply(w, nil, err)
		return
	}
	var owner *camp.UserContext
	if token := bearerToken(r); token != "" {
		p, err := a.Sessions.Broker.Auth.Authenticate(r.Context(), token)
		if err == nil && p.AgentID != "" {
			agent, err := a.Registrations.Identities.Agent(r.Context(), p.AgentID)
			if err == nil && agent.Status == camp.AgentActive {
				user, err := a.Registrations.Identities.User(r.Context(), agent.OwnerUserID)
				if err == nil && !user.Disabled {
					owner = &camp.UserContext{User: user}
				}
			}
		}
		if owner == nil && err != nil {
			// A presented-but-invalid credential never downgrades into
			// open registration: fail closed.
			reply(w, nil, camp.ErrDenied)
			return
		}
	}
	reg, err := a.Registrations.Register(r.Context(), owner, req)
	reply(w, reg, err)
}

// identity handles authenticated GET /v1/identity: reconnect verification
// for a saved credential.
func (a *API) identity(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	out := map[string]any{"subject": p.Subject, "role": p.Role, "spaces": p.Spaces, "scopes": p.Scopes}
	if p.AgentID != "" && a.Registrations != nil {
		agent, err := a.Registrations.Identities.Agent(r.Context(), p.AgentID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		user, err := a.Registrations.Identities.User(r.Context(), agent.OwnerUserID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		out["agent"] = agent
		out["user_id"] = user.ID
	}
	reply(w, out, nil)
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}
