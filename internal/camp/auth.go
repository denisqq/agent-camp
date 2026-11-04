package camp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"time"
)

// PrincipalStore is the control-plane credential database. It holds revocable
// token records; S3 never stores credentials, so memory backups cannot leak
// working access.
type PrincipalStore interface {
	Principal(ctx context.Context, hash string) (Principal, error)
	SavePrincipal(ctx context.Context, p Principal) error
}

type Principal struct {
	Subject    string    `json:"subject"`
	Role       string    `json:"role"`
	Spaces     []string  `json:"spaces"`
	Scopes     []string  `json:"scopes"`
	Expires    time.Time `json:"expires"`
	TokenHash  string    `json:"token_hash"`
	ParentHash string    `json:"parent_hash,omitempty"`
	Revoked    bool      `json:"revoked"`
}
type Auth struct{ Principals PrincipalStore }

func NewAuth(p PrincipalStore) *Auth      { return &Auth{p} }
func digest(s string) string              { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func contains(xs []string, x string) bool { return slices.Contains(xs, "*") || slices.Contains(xs, x) }

func (a *Auth) Bootstrap(ctx context.Context, token string) error {
	if len(token) < 32 {
		return errors.New("bootstrap token must contain at least 32 characters")
	}
	hash := digest(token)
	if _, err := a.Principals.Principal(ctx, hash); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return a.Principals.SavePrincipal(ctx, Principal{Subject: "system", Role: "admin", Spaces: []string{"*"}, Scopes: []string{"*"}, Expires: time.Now().AddDate(10, 0, 0), TokenHash: hash})
}
func (a *Auth) Authenticate(ctx context.Context, token string) (Principal, error) {
	return a.validate(ctx, digest(token), 0)
}
func (a *Auth) validate(ctx context.Context, hash string, depth int) (Principal, error) {
	var p Principal
	if depth > 8 || len(hash) != 64 {
		return p, ErrDenied
	}
	p, err := a.Principals.Principal(ctx, hash)
	if err != nil {
		return p, ErrDenied
	}
	if p.Revoked || !time.Now().Before(p.Expires) || p.TokenHash != hash {
		return p, ErrDenied
	}
	if p.ParentHash != "" {
		parent, err := a.validate(ctx, p.ParentHash, depth+1)
		if err != nil {
			return p, ErrDenied
		}
		for _, sp := range p.Spaces {
			if !contains(parent.Spaces, sp) {
				return p, ErrDenied
			}
		}
		for _, scope := range p.Scopes {
			if !contains(parent.Scopes, scope) {
				return p, ErrDenied
			}
		}
	}
	return p, nil
}
func (a *Auth) Current(ctx context.Context, p Principal) (Principal, error) {
	return a.validate(ctx, p.TokenHash, 0)
}
func (a *Auth) Issue(ctx context.Context, issuer Principal, p Principal) (string, error) {
	issuer, err := a.Current(ctx, issuer)
	if err != nil {
		return "", err
	}
	if p.Subject == "" || p.Expires.IsZero() || !time.Now().Before(p.Expires) || p.Expires.After(issuer.Expires) {
		return "", ErrInvalid
	}
	if issuer.Role != "admin" {
		if !contains(issuer.Scopes, "delegate") || p.Subject != issuer.Subject || p.Role != issuer.Role {
			return "", ErrDenied
		}
	}
	for _, sp := range p.Spaces {
		if !contains(issuer.Spaces, sp) {
			return "", ErrDenied
		}
	}
	for _, scope := range p.Scopes {
		if !contains(issuer.Scopes, scope) {
			return "", ErrDenied
		}
	}
	token := ID() + ID()
	p.TokenHash = digest(token)
	p.ParentHash = issuer.TokenHash
	p.Revoked = false
	return token, a.Principals.SavePrincipal(ctx, p)
}
func (a *Auth) Revoke(ctx context.Context, issuer Principal, hash string) error {
	issuer, err := a.Current(ctx, issuer)
	if err != nil {
		return err
	}
	p, err := a.Principals.Principal(ctx, hash)
	if err != nil {
		return ErrDenied
	}
	if issuer.Role != "admin" && issuer.TokenHash != hash && p.ParentHash != issuer.TokenHash {
		return ErrDenied
	}
	p.Revoked = true
	return a.Principals.SavePrincipal(ctx, p)
}

var roleRank = map[string]int{"": 0, "reader": 1, "commenter": 2, "writer": 3}
var roleActions = map[string]map[string]bool{
	"reader":    {"read": true},
	"commenter": {"read": true, "propose": true},
	"writer":    {"read": true, "propose": true, "write": true},
}

// DecideAccess resolves one action for a path. The caller supplies the
// effective section ACL (nil = no override: personal and team spaces default
// to closed, shared spaces are open). Shared spaces carry a fixed policy —
// read and comment for every principal, write for service roles only — so
// section overrides do not apply there.
func (a *Auth) DecideAccess(ctx context.Context, p Principal, space Space, acl *Access, action string) error {
	if p.TokenHash != "" {
		var err error
		p, err = a.Current(ctx, p)
		if err != nil {
			return err
		}
	}
	if !contains(p.Spaces, space.ID) || !contains(p.Scopes, action) {
		return ErrDenied
	}
	if space.Kind == "system" {
		if p.Role == "admin" {
			return nil
		}
		return ErrDenied
	}
	if p.Role == "admin" {
		return nil
	}
	if space.Kind == "shared" {
		if action == "read" || action == "propose" {
			return nil
		}
		if p.Role == "curator" || p.Role == "arbiter" {
			return nil
		}
		return ErrDenied
	}
	if p.Subject == space.Owner {
		return nil
	}
	if acl == nil {
		acl = space.Access
	}
	if acl == nil {
		return ErrDenied
	}
	role := ""
	if acl.Visibility == "open" {
		role = "reader"
	}
	for _, entry := range acl.Share {
		if entry.Subject == p.Subject && roleRank[entry.Role] > roleRank[role] {
			role = entry.Role
		}
	}
	if action == "collective:process" {
		for _, entry := range acl.Share {
			if entry.Subject == p.Subject && roleRank[entry.Role] >= 1 {
				return nil
			}
		}
		return ErrDenied
	}
	if roleActions[role][action] {
		return nil
	}
	return ErrDenied
}

func (b *Broker) AuthorizePath(ctx context.Context, p Principal, snap Snapshot, path, action string) error {
	if _, err := cleanPath(path); err != nil {
		return err
	}
	acl, err := b.Store.SectionACL(ctx, snap, path)
	if err != nil {
		return err
	}
	return b.Auth.DecideAccess(ctx, p, snap.Space, acl, action)
}
