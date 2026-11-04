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
	}
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
