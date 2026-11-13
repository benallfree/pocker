package mcp

import (
	"fmt"
	"net/url"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	oauthserver "github.com/pockethost/pocketone/ext/oauth-server"
	"github.com/pockethost/pocketone/ext/tokens"
)

// OAuth signs MCP requests in with access tokens from ext/oauth-server (API tokens from ext/tokens), the way Claude
// connectors and Claude Code expect: the client follows the 401 to the protected resource metadata, registers itself
// (CIMD or dynamic registration), signs the user in in a browser with PKCE, and gets a token bound to this endpoint
// (RFC 8707 audience) with the mcp:read and/or mcp:write scopes.
//
// It's the default [Authenticator] when oauth-server is registered on the app.
type OAuth struct {
	// Issuer overrides the authorization server listed in the protected resource metadata (default: oauth-server's
	// own issuer, i.e. its Config.Issuer or the request's scheme and host).
	Issuer string

	// AllowUnbound also accepts API tokens without an audience (made by hand or for deploys) and PocketBase auth
	// tokens, with full scope. Off by default: such a token wasn't issued for MCP.
	AllowUnbound bool
}

// Authenticate implements [Authenticator].
func (a OAuth) Authenticate(e *core.RequestEvent, resource string) (*Grant, error) {
	if e.Auth == nil {
		if strings.TrimSpace(e.Request.Header.Get("Authorization")) == "" {
			return nil, ErrNoCredentials
		}
		return nil, ErrInvalidToken
	}
	info := tokens.FromRequest(e)
	if info == nil || info.Audience == "" {
		if a.AllowUnbound {
			return &Grant{Auth: e.Auth, Scopes: []string{ScopeRead, ScopeWrite}}, nil
		}
		return nil, fmt.Errorf("%w: the token wasn't issued for this MCP endpoint", ErrInvalidToken)
	}
	if !sameResource(info.Audience, resource) {
		return nil, fmt.Errorf("%w: the token was issued for %s", ErrInvalidToken, info.Audience)
	}
	g := &Grant{Auth: info.Owner, ClientID: info.Token.GetString("client_id")}
	if len(info.Scopes) == 0 {
		g.Scopes = []string{ScopeRead, ScopeWrite} // offline_access only: the account's full rights, limited by the audience
	}
	for _, s := range info.Scopes {
		if s == ScopeRead || s == ScopeWrite {
			g.Scopes = append(g.Scopes, s)
		}
	}
	return g, nil
}

// AuthorizationServers implements [Authenticator].
func (a OAuth) AuthorizationServers(e *core.RequestEvent, s *Server) []string {
	if a.Issuer != "" {
		return []string{strings.TrimRight(a.Issuer, "/")}
	}
	if osrv := oauthserver.For(s.app); osrv != nil {
		return []string{osrv.Issuer(e)}
	}
	scheme := "http"
	if e.IsTLS() || strings.EqualFold(e.Request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return []string{scheme + "://" + e.Request.Host}
}

// sameResource compares two resource URLs: scheme and host ignore case, a trailing slash doesn't matter.
func sameResource(a, b string) bool {
	ua, err1 := url.Parse(strings.TrimRight(a, "/"))
	ub, err2 := url.Parse(strings.TrimRight(b, "/"))
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host) && ua.Path == ub.Path
}

// setupOAuth runs on serve, after every plugin has registered: it picks the authenticator, registers the MCP
// scopes with ext/tokens and turns on dynamic client registration when OAuth signs MCP in.
func (s *Server) setupOAuth() {
	osrv := oauthserver.For(s.app)
	if s.cfg.Auth == nil {
		if osrv != nil && tokens.For(s.app) != nil {
			s.cfg.Auth = OAuth{}
		} else {
			s.cfg.Auth = PocketBaseTokenAuth{}
			s.app.Logger().Warn("mcp: ext/oauth-server isn't registered; /mcp accepts superuser PocketBase tokens only, and clients can't sign in by themselves")
		}
	}
	if t := tokens.For(s.app); t != nil {
		t.RegisterScope(tokens.Scope{Name: ScopeRead, Description: "Use read-only MCP tools", Paths: []string{s.cfg.Path}})
		t.RegisterScope(tokens.Scope{Name: ScopeWrite, Description: "Use every MCP tool you're allowed to, including ones that change data", Paths: []string{s.cfg.Path}})
	}
	if _, ok := s.cfg.Auth.(OAuth); ok && osrv != nil {
		osrv.EnableDynamicRegistration()
	}
}
