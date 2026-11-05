package camp

import (
	"context"
	"strconv"
	"sync"
)

// MemState is an in-memory control-plane store for tests and embedded single
// process use. Durable deployments use the SQLite-backed control store.
type MemState struct {
	mu         sync.Mutex
	principals map[string]Principal
	sessions   map[string]memVersioned[SessionState]
	executions map[string]memVersioned[ExecutionState]
	users      map[string]User
	agents     map[string]Agent
}

type memVersioned[T any] struct {
	value   T
	version int64
}

func NewMemState() *MemState {
	return &MemState{
		principals: map[string]Principal{},
		sessions:   map[string]memVersioned[SessionState]{},
		executions: map[string]memVersioned[ExecutionState]{},
		users:      map[string]User{},
		agents:     map[string]Agent{},
	}
}

// MemState also implements IdentityStore for tests and embedded use.

func (m *MemState) CreateUser(ctx context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.ID]; ok {
		return ErrConflict
	}
	m.users[u.ID] = u
	return nil
}
func (m *MemState) SaveUser(ctx context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.ID]; !ok {
		return ErrNotFound
	}
	m.users[u.ID] = u
	return nil
}
func (m *MemState) User(ctx context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}
func (m *MemState) CreateAgent(ctx context.Context, a Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[a.ID]; ok {
		return ErrConflict
	}
	if _, ok := m.users[a.OwnerUserID]; !ok {
		return ErrInvalid
	}
	m.agents[a.ID] = a
	return nil
}
func (m *MemState) SaveAgent(ctx context.Context, a Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.agents[a.ID]
	if !ok {
		return ErrNotFound
	}
	if a.OwnerUserID != current.OwnerUserID {
		return ErrInvalid // ownership is immutable
	}
	m.agents[a.ID] = a
	return nil
}
func (m *MemState) Agent(ctx context.Context, id string) (Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	if !ok {
		return Agent{}, ErrNotFound
	}
	return a, nil
}
func (m *MemState) AgentsByOwner(ctx context.Context, userID string) ([]Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Agent
	for _, a := range m.agents {
		if a.OwnerUserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MemState) Principal(ctx context.Context, hash string) (Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.principals[hash]
	if !ok {
		return Principal{}, ErrNotFound
	}
	return p, nil
}
func (m *MemState) SavePrincipal(ctx context.Context, p Principal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.principals[p.TokenHash] = p
	return nil
}

func (m *MemState) Session(ctx context.Context, id string) (SessionState, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return SessionState{}, "", ErrNotFound
	}
	return s.value, memTag(s.version), nil
}
func (m *MemState) SaveSession(ctx context.Context, s *SessionState, expect string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.sessions[s.ID]
	if expect == "" {
		if ok {
			return "", ErrConflict
		}
		m.sessions[s.ID] = memVersioned[SessionState]{*s, 1}
		return memTag(1), nil
	}
	if !ok || memTag(current.version) != expect {
		return "", ErrConflict
	}
	m.sessions[s.ID] = memVersioned[SessionState]{*s, current.version + 1}
	return memTag(current.version + 1), nil
}

func (m *MemState) Execution(ctx context.Context, key string) (ExecutionState, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.executions[key]
	if !ok {
		return ExecutionState{}, "", ErrNotFound
	}
	return x.value, memTag(x.version), nil
}
func (m *MemState) CreateExecution(ctx context.Context, key string, x ExecutionState) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.executions[key]; ok {
		return "", ErrConflict
	}
	m.executions[key] = memVersioned[ExecutionState]{x, 1}
	return memTag(1), nil
}
func (m *MemState) SaveExecution(ctx context.Context, key string, x ExecutionState, expect string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.executions[key]
	if !ok || memTag(current.version) != expect {
		return ErrConflict
	}
	m.executions[key] = memVersioned[ExecutionState]{x, current.version + 1}
	return nil
}

func memTag(v int64) string {
	if v <= 0 {
		return "0"
	}
	return strconv.FormatInt(v, 10)
}
