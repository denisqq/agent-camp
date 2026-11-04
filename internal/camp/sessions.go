package camp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Callback func(context.Context, string, map[string]any) (any, error)
type RunOutput struct {
	Value  any    `json:"result"`
	Stdout string `json:"stdout"`
}
type Executor interface {
	Execute(context.Context, string, Callback) (RunOutput, error)
}

// RunStateStore persists sessions and execution receipts. It is control-plane
// state: durable across restarts for idempotency, but never part of the S3
// memory plane. Tags are optimistic concurrency versions.
type RunStateStore interface {
	Session(ctx context.Context, id string) (SessionState, string, error)
	SaveSession(ctx context.Context, s *SessionState, expect string) (string, error)
	Execution(ctx context.Context, key string) (ExecutionState, string, error)
	CreateExecution(ctx context.Context, key string, x ExecutionState) (string, error)
	SaveExecution(ctx context.Context, key string, x ExecutionState, expect string) error
}

type StartRequest struct {
	Space     string `json:"primary_space"`
	Task      string `json:"task,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}
type RunRequest struct {
	SessionID   string `json:"session_id"`
	OperationID string `json:"operation_id"`
	Code        string `json:"code"`
	DryRun      bool   `json:"dry_run,omitempty"`
}
type SessionView struct {
	ID         string         `json:"session_id"`
	Snapshot   string         `json:"snapshot"`
	SDKVersion string         `json:"sdk_version"`
	Help       map[string]any `json:"help"`
}
type SessionState struct {
	ID         string              `json:"id"`
	Principal  string              `json:"principal"`
	Primary    string              `json:"primary"`
	Snapshots  map[string]Snapshot `json:"snapshots"`
	CursorKey  []byte              `json:"cursor_key"`
	Created    time.Time           `json:"created"`
	LastUsed   time.Time           `json:"last_used"`
	Active     string              `json:"active"`
	Lease      time.Time           `json:"lease"`
	SDKVersion string              `json:"sdk_version"`
}
type ExecutionState struct {
	Receipt  Receipt   `json:"receipt"`
	Deadline time.Time `json:"deadline"`
}
type Sessions struct {
	Broker   *Broker
	Executor Executor
	State    RunStateStore
	Now      func() time.Time
	Timeout  time.Duration
}

func NewSessions(b *Broker, x Executor) *Sessions {
	return &Sessions{Broker: b, Executor: x, State: NewMemState(), Now: time.Now, Timeout: 30 * time.Second}
}

func (s *Sessions) Start(ctx context.Context, p Principal, r StartRequest) (SessionView, error) {
	if r.SessionID != "" {
		st, _, err := s.load(ctx, p, r.SessionID)
		if err != nil {
			return SessionView{}, err
		}
		return sessionView(st), nil
	}
	e, err := s.Broker.Begin(ctx, p, r.Space)
	if err != nil {
		return SessionView{}, err
	}
	now := s.Now()
	st := SessionState{ID: ID(), Principal: p.TokenHash, Primary: r.Space, Snapshots: e.Snapshots, CursorKey: e.CursorKey, Created: now, LastUsed: now, SDKVersion: "v1"}
	if _, err = s.State.SaveSession(ctx, &st, ""); err != nil {
		return SessionView{}, err
	}
	return sessionView(st), nil
}
func sessionView(st SessionState) SessionView {
	help := SDKHelp("")
	help["primary_space"] = st.Primary
	help["examples"] = map[string]string{
		"catalog": "from brain_sdk import brain\nbrain.ls('/', limit=10)",
		"rules":   "from brain_sdk import brain\nbrain.find(kind='constitution', limit=5)",
		"read":    "from brain_sdk import brain\nbrain.read('/path/from/catalog', start_line=1, limit=40)",
		"help":    "from brain_sdk import brain\nbrain.help('navigation')",
	}
	help["workflow"] = "Use run(session_id, operation_id, code) with these examples. Discover rules in this space and any relevant shared space returned by map(); read returned rule paths before applied methods. Catalog entries contain paths and metadata; replace the read example path with one returned by the catalog. Read progressively; check truncated/coverage and follow cursors. Use a new operation_id for new code; identical retries reuse it."
	return SessionView{st.ID, st.Snapshots[st.Primary].Commit, st.SDKVersion, help}
}
func (s *Sessions) load(ctx context.Context, p Principal, id string) (SessionState, string, error) {
	if !safeID.MatchString(id) {
		return SessionState{}, "", ErrInvalid
	}
	if _, err := s.Broker.Auth.Current(ctx, p); err != nil {
		return SessionState{}, "", err
	}
	st, tag, err := s.State.Session(ctx, id)
	if err != nil {
		return SessionState{}, "", ErrDenied
	}
	if st.Principal != p.TokenHash {
		return SessionState{}, "", ErrDenied
	}
	now := s.Now()
	if now.Sub(st.Created) > 8*time.Hour || now.Sub(st.LastUsed) > 30*time.Minute {
		return SessionState{}, "", errors.New("session expired")
	}
	if st.SDKVersion != "v1" {
		return SessionState{}, "", errors.New("unsupported session SDK")
	}
	return st, tag, nil
}
func (s *Sessions) returnReceipt(ctx context.Context, p Principal, r Receipt) (Receipt, error) {
	if _, err := s.Broker.Auth.Current(ctx, p); err != nil {
		return Receipt{}, err
	}
	if err := s.Broker.CheckDependencies(ctx, p, r.Dependencies); err != nil {
		return Receipt{}, err
	}
	r.Dependencies = nil
	r.Fingerprint = ""
	return r, nil
}
func (s *Sessions) Run(ctx context.Context, p Principal, r RunRequest) (Receipt, error) {
	local := *s
	local.Broker = s.Broker.withReadCache()
	s = &local
	if len(r.Code) > 65536 || r.Code == "" || !safeID.MatchString(r.OperationID) {
		return Receipt{}, ErrInvalid
	}
	st, stag, err := s.load(ctx, p, r.SessionID)
	if err != nil {
		return Receipt{}, err
	}
	fingerprint := digest(fmt.Sprintf("%s\x00%s\x00%s\x00%t", st.SDKVersion, p.TokenHash, r.Code, r.DryRun))
	op := digest(st.ID + "/" + r.OperationID)
	objectKey := "executions/" + st.ID + "/" + op
	current, err := s.Broker.Store.Load(ctx, st.Primary)
	if err != nil {
		return Receipt{}, err
	}
	if prior, e := s.Broker.Store.Operation(ctx, current, op); e == nil {
		if prior.Fingerprint != fingerprint {
			return Receipt{}, ErrConflict
		}
		return s.returnReceipt(ctx, p, prior)
	} else if !errors.Is(e, ErrNotFound) {
		return Receipt{}, e
	}
	if previous, _, e := s.State.Execution(ctx, objectKey); e == nil {
		if previous.Receipt.Fingerprint != fingerprint {
			return Receipt{}, ErrConflict
		}
		if previous.Receipt.Status == "pending" && s.Now().After(previous.Deadline) {
			previous.Receipt.Status = "failed"
			previous.Receipt.Error = "execution interrupted; use a new operation id after checking state"
		}
		return s.returnReceipt(ctx, p, previous.Receipt)
	} else if !errors.Is(e, ErrNotFound) {
		return Receipt{}, e
	}
	if st.Active != "" && s.Now().Before(st.Lease) {
		return Receipt{}, ErrConflict
	}
	st.Active = op
	st.Lease = s.Now().Add(s.Timeout + 5*time.Second)
	st.LastUsed = s.Now()
	if stag, err = s.State.SaveSession(ctx, &st, stag); err != nil {
		return Receipt{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		st.Active = ""
		st.LastUsed = s.Now()
		_, _ = s.State.SaveSession(cleanup, &st, stag)
	}()
	pending := ExecutionState{Receipt: Receipt{Status: "pending", Fingerprint: fingerprint}, Deadline: st.Lease}
	etag, err := s.State.CreateExecution(ctx, objectKey, pending)
	if err != nil {
		return Receipt{}, err
	}
	e := &Execution{Principal: p, Primary: st.Primary, Snapshots: st.Snapshots, Staged: map[string]Revision{}, CursorKey: st.CursorKey}
	runctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	output, runerr := s.Executor.Execute(runctx, r.Code, func(c context.Context, m string, a map[string]any) (any, error) { return s.Broker.Call(c, e, m, a) })
	result := Receipt{Status: "completed", Result: output.Value, Stdout: output.Stdout, Fingerprint: fingerprint, Dependencies: e.Dependencies}
	if runerr == nil {
		encoded, e := encode(output)
		if e != nil || len(encoded) > 16384 {
			runerr = errors.New("result must be JSON within 16 KiB")
		}
	}
	if runerr == nil {
		runerr = runctx.Err()
	}
	if runerr == nil {
		runerr = s.Broker.CheckDependencies(runctx, p, e.Dependencies)
	}
	if runerr != nil {
		result.Status = "failed"
		result.Result = nil
		result.Stdout = ""
		result.Error = "execution failed"
		if errors.Is(runerr, context.DeadlineExceeded) || errors.Is(runctx.Err(), context.DeadlineExceeded) {
			result.Status = "timeout"
		}
	}
	if runerr == nil && (r.DryRun || len(e.Staged) == 0) {
		result.Status = "no_changes"
	} else if runerr == nil {
		accepted, commitErr := s.Broker.Finish(runctx, e, op, result)
		if commitErr == nil {
			result.ChangeID = accepted.Commit
			st.Snapshots[st.Primary] = accepted
		} else {
			latest, le := s.Broker.Store.Load(ctx, st.Primary)
			if le == nil {
				saved, se := s.Broker.Store.Operation(ctx, latest, op)
				if se == nil {
					result = saved
					st.Snapshots[st.Primary] = latest
					commitErr = nil
				}
			}
			if commitErr != nil {
				result.Status = "failed"
				result.Result = nil
				result.Stdout = ""
				result.Error = "commit not accepted"
				if errors.Is(commitErr, ErrConflict) {
					result.Status = "conflict"
				}
			}
		}
	}
	finalctx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if err = s.State.SaveExecution(finalctx, objectKey, ExecutionState{Receipt: result, Deadline: pending.Deadline}, etag); err != nil {
		return Receipt{}, err
	}
	return s.returnReceipt(finalctx, p, result)
}
