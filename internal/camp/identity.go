package camp

import (
	"context"
	"time"
)

// User is a Camp-local identity owning one or more agents. IDs are stable
// server-generated values; display names are labels and never ownership
// proof. Users live in the control plane, never in workspace memory.
type User struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Disabled    bool      `json:"disabled,omitempty"`
}

// Agent is a registered acting identity owned by exactly one user, for
// ever: ownership is immutable so historical attribution stays readable.
// Client and DisplayName are self-reported labels; the credential, not a
// name, authenticates the agent.
type Agent struct {
	ID          string    `json:"id"`
	OwnerUserID string    `json:"owner_user_id"`
	DisplayName string    `json:"display_name,omitempty"`
	Client      string    `json:"client,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	// Status is "active" or "revoked". Revoked agents keep their authored
	// documents and attribution.
	Status string `json:"status"`
}

// UserContext is an established internal user, resolved by an
// authentication source before any registration or management operation.
// It is the narrow boundary a future identity provider plugs into: the
// provider establishes this context; Camp decides what the user may do.
type UserContext struct {
	User User
}

// IdentityStore persists users and agents with their ownership
// relationships. Implementations must enforce that an agent's owner exists
// and that ownership never changes for an existing agent ID.
type IdentityStore interface {
	CreateUser(ctx context.Context, u User) error
	SaveUser(ctx context.Context, u User) error
	User(ctx context.Context, id string) (User, error)
	CreateAgent(ctx context.Context, a Agent) error
	SaveAgent(ctx context.Context, a Agent) error
	Agent(ctx context.Context, id string) (Agent, error)
	AgentsByOwner(ctx context.Context, userID string) ([]Agent, error)
}
