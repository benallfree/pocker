package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// Grant is what a valid credential allows.
type Grant struct {
	// Auth is the signed-in record.
	Auth *core.Record
	// Scopes granted to the client (see [ScopeRead], [ScopeWrite]).
	Scopes []string
	// ClientID of the OAuth client, when the credential came from one.
	ClientID string
}

func (g *Grant) has(scope string) bool {
	if g == nil {
		return false
	}
	if slices.Contains(g.Scopes, scope) {
		return true
	}
	return scope == ScopeRead && slices.Contains(g.Scopes, ScopeWrite)
}

var (
	// ErrNoCredentials means the request carried none: the client gets a 401 that points it at sign-in.
	ErrNoCredentials = errors.New("no credentials")
	// ErrInvalidToken means the credential is unknown, expired, revoked or for another audience.
	ErrInvalidToken = errors.New("invalid token")
)

// Authenticator signs MCP requests in. ext/oauth-server provides the real one; [PocketBaseTokenAuth] is the
// interim one.
type Authenticator interface {
	// Authenticate checks the request's credentials for resource (the endpoint's canonical URL, which an OAuth token
	// must have been issued for). Return [ErrNoCredentials] or [ErrInvalidToken] (possibly wrapped) to send a 401.
	Authenticate(e *core.RequestEvent, resource string) (*Grant, error)

	// AuthorizationServers are the issuers listed in the protected resource metadata, primary first (Claude uses only
	// the first).
	AuthorizationServers(e *core.RequestEvent, s *Server) []string
}

// PocketBaseTokenAuth accepts ordinary PocketBase auth tokens (Authorization: Bearer <token>, e.g. one minted with
// the impersonate API) for records of the listed collections (default: superusers only), with full scope.
//
// Interim: it exists so the endpoint works before ext/oauth-server does, and for tests. Tokens aren't
// audience-bound or scoped, and clients can't sign in by themselves with it.
type PocketBaseTokenAuth struct {
	Collections []string
}

// Authenticate implements [Authenticator].
func (a PocketBaseTokenAuth) Authenticate(e *core.RequestEvent, _ string) (*Grant, error) {
	if e.Auth == nil {
		if strings.TrimSpace(e.Request.Header.Get("Authorization")) == "" {
			return nil, ErrNoCredentials
		}
		return nil, ErrInvalidToken
	}
	cols := a.Collections
	if len(cols) == 0 {
		cols = []string{core.CollectionNameSuperusers}
	}
	if !slices.Contains(cols, e.Auth.Collection().Name) {
		return nil, fmt.Errorf("%w: %s records can't use MCP", ErrInvalidToken, e.Auth.Collection().Name)
	}
	return &Grant{Auth: e.Auth, Scopes: []string{ScopeRead, ScopeWrite}}, nil
}

// AuthorizationServers implements [Authenticator]: the app itself, where ext/oauth-server will answer.
func (a PocketBaseTokenAuth) AuthorizationServers(e *core.RequestEvent, s *Server) []string {
	return []string{s.publicOrigin(e)}
}

const wellKnownPRM = "/.well-known/oauth-protected-resource"

// MetadataURL is where this endpoint's protected resource metadata (RFC 9728) is served.
func (s *Server) MetadataURL(e *core.RequestEvent) string {
	return s.publicOrigin(e) + wellKnownPRM + s.cfg.Path
}

// ProtectedResourceMetadata is the RFC 9728 document for this endpoint.
func (s *Server) ProtectedResourceMetadata(e *core.RequestEvent) map[string]any {
	name := s.app.Settings().Meta.AppName
	if name == "" {
		name = "PocketBase"
	}
	return map[string]any{
		"resource":                 s.ResourceURL(e),
		"authorization_servers":    s.cfg.Auth.AuthorizationServers(e, s),
		"scopes_supported":         []string{ScopeRead, ScopeWrite},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            name + " MCP",
	}
}

// challenge answers 401 with the WWW-Authenticate header MCP clients (Claude) need to find the sign-in.
func (s *Server) challenge(e *core.RequestEvent, err error) error {
	v := fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, s.MetadataURL(e), ScopeRead+" "+ScopeWrite)
	msg := "Sign-in required."
	if err != nil && !errors.Is(err, ErrNoCredentials) {
		v += `, error="invalid_token"`
		msg = "The access token is invalid or expired."
	}
	e.Response.Header().Set("WWW-Authenticate", v)
	return e.JSON(http.StatusUnauthorized, map[string]any{"error": "unauthorized", "message": msg})
}

// insufficientScope answers 403 so the client can ask for more (step-up).
func (s *Server) insufficientScope(e *core.RequestEvent, id any, scope string) error {
	e.Response.Header().Set("WWW-Authenticate", fmt.Sprintf(
		`Bearer error="insufficient_scope", scope=%q, resource_metadata=%q`, scope, s.MetadataURL(e)))
	return e.JSON(http.StatusForbidden, rpcError(id, codeInvalidParams, "This tool needs the "+scope+" scope."))
}
