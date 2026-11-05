package api

import (
	"github.com/denisqq/agent-camp/internal/camp"
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"io"
	"net/http"
)

type API struct {
	Sessions *camp.Sessions
	// Registrations, when set, enables POST /v1/agents/register and the
	// agent details in GET /v1/identity.
	Registrations *camp.RegistrationService
	// Fallback authenticates stdio MCP clients, where no HTTP bearer header
	// exists: the principal is resolved from CAMP_TOKEN at startup.
	Fallback func(context.Context) (camp.Principal, error)
}

// NewAPI exposes the API type for transports other than HTTP (stdio MCP).
func NewAPI(s *camp.Sessions) *API { return &API{Sessions: s} }

// New builds the HTTP handler. An optional RegistrationService enables
// POST /v1/agents/register and agent details in GET /v1/identity.
func New(s *camp.Sessions, registrations ...*camp.RegistrationService) http.Handler {
	a := &API{Sessions: s}
	if len(registrations) > 0 {
		a.Registrations = registrations[0]
	}
	router := chi.NewRouter()
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) { reply(w, map[string]string{"status": "ok"}, nil) })
	verify := func(ctx context.Context, token string, request *http.Request) (*mcpauth.TokenInfo, error) {
		p, err := s.Broker.Auth.Authenticate(ctx, token)
		if err != nil {
			// Revoked, expired, or owner-disabled credentials are
			// authentication failures: report 401, never a server error.
			return nil, mcpauth.ErrInvalidToken
		}
		return &mcpauth.TokenInfo{UserID: p.TokenHash, Expiration: p.Expires, Scopes: p.Scopes, Extra: map[string]any{"principal": p}}, nil
	}
	router.Group(func(r chi.Router) {
		r.Use(mcpauth.RequireBearerToken(verify, nil))
		r.Handle("/mcp", a.mcp())
		r.Post("/v1/sessions/start", a.start)
		r.Post("/v1/sessions/{session}/run", a.run)
		r.Get("/v1/identity", a.identity)
		r.Post("/v1/admin/spaces", a.createSpace)
		r.Get("/v1/admin/spaces", a.listSpaces)
		r.Get("/v1/admin/spaces/{space}", a.getSpace)
		r.Post("/v1/admin/tokens", a.issue)
		r.Delete("/v1/admin/tokens/{hash}", a.revoke)
		r.Put("/v1/admin/spaces/{space}/access", a.access)
		r.Post("/v1/files/upload", a.upload)
		r.Get("/v1/files/download", a.download)
	})
	// Open registration is reachable without a credential when (and only
	// when) the server policy explicitly enables it.
	router.Post("/v1/agents/register", a.register)
	return http.NewCrossOriginProtection().Handler(router)
}
func principal(ctx context.Context) (camp.Principal, error) {
	info := mcpauth.TokenInfoFromContext(ctx)
	if info == nil {
		return camp.Principal{}, camp.ErrDenied
	}
	p, ok := info.Extra["principal"].(camp.Principal)
	if !ok {
		return p, camp.ErrDenied
	}
	return p, nil
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return camp.ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return camp.ErrInvalid
	}
	return nil
}
func reply(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		status := http.StatusInternalServerError
		message := "operation unavailable"
		switch {
		case errors.Is(err, camp.ErrRegistrationDisabled):
			status = http.StatusForbidden
			message = "agent registration is disabled on this server"
		case errors.Is(err, camp.ErrDenied):
			status = 403
			message = "access denied"
		case errors.Is(err, camp.ErrInvalid):
			status = 400
			message = "invalid request"
		case errors.Is(err, camp.ErrNotFound):
			status = 404
			message = "not found"
		case errors.Is(err, camp.ErrConflict):
			status = 409
			message = "conflict"
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
func (a *API) start(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	var req camp.StartRequest
	if err = decode(w, r, &req); err != nil {
		reply(w, nil, err)
		return
	}
	out, err := a.Sessions.Start(r.Context(), p, req)
	reply(w, out, err)
}
func (a *API) run(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	var req camp.RunRequest
	if err = decode(w, r, &req); err != nil {
		reply(w, nil, err)
		return
	}
	req.SessionID = chi.URLParam(r, "session")
	out, err := a.Sessions.Run(r.Context(), p, req)
	reply(w, out, err)
}
func (a *API) createSpace(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil || p.Role != "admin" {
		reply(w, nil, camp.ErrDenied)
		return
	}
	var req camp.Space
	if err = decode(w, r, &req); err != nil {
		reply(w, nil, err)
		return
	}
	if err = a.Sessions.Broker.Auth.DecideAccess(r.Context(), p, req, nil, "write"); err != nil {
		reply(w, nil, err)
		return
	}
	out, err := a.Sessions.Broker.Store.CreateSpace(r.Context(), req)
	reply(w, out.Space, err)
}
func (a *API) issue(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	var req camp.Principal
	if err = decode(w, r, &req); err != nil {
		reply(w, nil, err)
		return
	}
	token, err := a.Sessions.Broker.Auth.Issue(r.Context(), p, req)
	reply(w, map[string]string{"token": token}, err)
}
func (a *API) revoke(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err == nil {
		err = a.Sessions.Broker.Auth.Revoke(r.Context(), p, chi.URLParam(r, "hash"))
	}
	reply(w, map[string]bool{"revoked": err == nil}, err)
}
func (a *API) access(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil || p.Role != "admin" {
		reply(w, nil, camp.ErrDenied)
		return
	}
	var req struct {
		Access   *camp.Access `json:"access"`
		Policy   string        `json:"policy"`
		Expected string        `json:"expected_commit"`
	}
	if err = decode(w, r, &req); err != nil {
		reply(w, nil, err)
		return
	}
	if err = a.Sessions.Broker.Auth.DecideAccess(r.Context(), p, camp.Space{ID: chi.URLParam(r, "space")}, nil, "write"); err != nil {
		reply(w, nil, err)
		return
	}
	base, err := a.Sessions.Broker.Store.Load(r.Context(), chi.URLParam(r, "space"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	if base.Commit != req.Expected {
		reply(w, nil, camp.ErrConflict)
		return
	}
	space := base.Space
	if err = camp.ValidateAccess(req.Access); err != nil {
		reply(w, nil, err)
		return
	}
	if base.Space.Kind == "shared" && req.Access != nil {
		reply(w, nil, camp.ErrDenied)
		return
	}
	space.Access = req.Access
	if req.Policy != "" {
		space.Policy = req.Policy
	}
	out, err := a.Sessions.Broker.Store.Commit(r.Context(), base, nil, camp.ID(), camp.Receipt{Status: "completed"}, &space)
	reply(w, out.Space, err)
}

func (a *API) listSpaces(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil || p.Role != "admin" {
		reply(w, nil, camp.ErrDenied)
		return
	}
	spaces, err := a.Sessions.Broker.Store.Spaces(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	allowed := []camp.Space{}
	for _, space := range spaces {
		if a.Sessions.Broker.Auth.DecideAccess(r.Context(), p, space, nil, "read") == nil {
			allowed = append(allowed, space)
		}
	}
	reply(w, allowed, nil)
}
func (a *API) getSpace(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil || p.Role != "admin" {
		reply(w, nil, camp.ErrDenied)
		return
	}
	if err = a.Sessions.Broker.Auth.DecideAccess(r.Context(), p, camp.Space{ID: chi.URLParam(r, "space")}, nil, "read"); err != nil {
		reply(w, nil, err)
		return
	}
	snap, err := a.Sessions.Broker.Store.Load(r.Context(), chi.URLParam(r, "space"))
	reply(w, snap.Space, err)
}
