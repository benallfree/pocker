// Package oauthserver makes a PocketBase app an OAuth 2.1 authorization server whose access tokens are API tokens
// from ext/tokens. `pocketbase login` signs in through it in the browser (loopback redirect + PKCE) or with a device
// code, and MCP clients (Claude, ChatGPT connectors) can register themselves and connect.
//
//	tokens.MustRegister(app, tokens.Config{})
//	oauthserver.MustRegister(app, oauthserver.Config{})
//
// Endpoints: /.well-known/oauth-authorization-server (RFC 8414), /.well-known/oauth-protected-resource (RFC 9728),
// /oauth/authorize (sign-in + consent pages), /oauth/token (authorization code + PKCE S256, refresh, device code),
// /oauth/device (RFC 8628), /oauth/revoke (RFC 7009), and /oauth/register (RFC 7591, off by default).
package oauthserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
	"github.com/pockethost/pocketone/ext/remotes"
	"github.com/pockethost/pocketone/ext/tokens"
)

const (
	storeKey = "oauthServer"
	paramKey = "oauth_server"

	// CLIClientId is the built-in public client `pocketbase login` uses: loopback redirects and device codes.
	CLIClientId = "pocketbase-cli"

	// MetadataPath is advertised in /_pocker/whoami as the "oauth" feature.
	MetadataPath = "/.well-known/oauth-authorization-server"
)

// Branding styles the built-in pages.
type Branding struct {
	// Name shown on the pages (default: Settings → Application name).
	Name string
	// LogoURL, if set, is shown above the form.
	LogoURL string
	// Accent is the button color (CSS color, default #16161a).
	Accent string
	// Footer is plain text under the form.
	Footer string
}

// Config is set by whoever builds the binary.
type Config struct {
	// Collections are the auth collections (names) whose records may sign in and authorize clients. Default:
	// _superusers. Their records must also be allowed to own API tokens (tokens.Config.Collections).
	Collections []string

	// AccessTTL is an access token's lifetime (default 30 days); RefreshTTL a refresh token's (default 180 days,
	// renewed on each use).
	AccessTTL  time.Duration
	RefreshTTL time.Duration

	// DynamicRegistration turns on POST /oauth/register (RFC 7591), e.g. for MCP connectors. A plugin can also turn
	// it on later with [Server.EnableDynamicRegistration].
	DynamicRegistration bool

	// CIMDHosts are the hosts whose https client ids are trusted as client ID metadata documents (the client id is
	// the URL of a JSON document with its name and redirect URIs). Default: claude.ai, claude.com. Set to an empty,
	// non-nil slice to turn CIMD off.
	CIMDHosts []string

	// HTTPClient fetches client metadata documents (default: 5 s timeout).
	HTTPClient *http.Client

	// Issuer is the server's public base URL (default: Settings → Application URL, else the request's own URL).
	Issuer string

	Branding Branding

	// Render, if set, renders a page instead of the built-in templates (return handled=false to fall back). When it
	// isn't set and ext/pages is registered with a RenderTemplate method, pages render "oauth/<page>" templates.
	Render func(e *core.RequestEvent, page string, data PageData) (handled bool, err error)

	// Now is the clock (tests).
	Now func() time.Time
}

// Server is the OAuth server of one app. Get it with [For].
type Server struct {
	app core.App
	cfg Config

	mu     sync.Mutex
	secret []byte

	limiter *limiter

	dcr  atomic.Bool
	cimd sync.Map // client id URL -> cimdEntry
}

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, cfg Config) *Server {
	s, err := Register(app, cfg)
	if err != nil {
		panic(err)
	}
	return s
}

// Register adds the OAuth server to app: its collections (on bootstrap), endpoints and pages (on serve), the
// "oauth" feature in /_pocker/whoami, and on a *pocketbase.PocketBase the `oauth clients` command. The app must
// also register ext/tokens.
func Register(app core.App, cfg Config) (*Server, error) {
	if len(cfg.Collections) == 0 {
		cfg.Collections = []string{core.CollectionNameSuperusers}
	}
	if cfg.AccessTTL <= 0 {
		cfg.AccessTTL = 30 * 24 * time.Hour
	}
	if cfg.RefreshTTL <= 0 {
		cfg.RefreshTTL = 180 * 24 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.CIMDHosts == nil {
		cfg.CIMDHosts = []string{"claude.ai", "claude.com"}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	s := &Server{app: app, cfg: cfg, limiter: newLimiter(10, 5*time.Minute)}
	s.dcr.Store(cfg.DynamicRegistration)
	app.Store().Set(storeKey, s)
	remotes.Advertise(app, "oauth", MetadataPath)

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if tokens.For(e.App) == nil {
			return errors.New("oauth-server needs ext/tokens: register tokens.MustRegister(app, ...) too")
		}
		return EnsureSchema(e.App)
	})
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		s.bindRoutes(se)
		return se.Next()
	})
	app.Cron().MustAdd("__pbOAuthCleanup__", "*/30 * * * *", func() {
		if err := s.deleteExpired(); err != nil {
			app.Logger().Warn("oauth-server: cleanup", "error", err)
		}
	})
	if pb, ok := app.(*pocketbase.PocketBase); ok {
		pb.RootCmd.AddCommand(NewCommand(s))
	}
	return s, nil
}

// For returns the OAuth server registered on app, or nil.
func For(app core.App) *Server {
	s, _ := app.Store().Get(storeKey).(*Server)
	return s
}

func (s *Server) tokens() *tokens.Tokens { return tokens.For(s.app) }

// EnableDynamicRegistration turns on POST /oauth/register (e.g. ext/mcp does when it registers).
func (s *Server) EnableDynamicRegistration() { s.dcr.Store(true) }

// OfflineAccess is accepted in scope requests (clients ask for it to get refresh tokens, which this server always
// issues); it isn't stored on tokens.
const OfflineAccess = "offline_access"

func (s *Server) allowedCollection(c *core.Collection) bool {
	return c != nil && c.IsAuth() && slices.Contains(s.cfg.Collections, c.Name)
}

// key returns the server's HMAC key (generated once, stored in _params).
func (s *Server) key() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secret != nil {
		return s.secret
	}
	param := &core.Param{}
	var stored struct {
		Secret string `json:"secret"`
	}
	if err := s.app.ModelQuery(param).Model(paramKey, param); err == nil {
		_ = json.Unmarshal(param.Value, &stored)
	} else {
		param = &core.Param{}
		param.Id = paramKey
		param.MarkAsNew()
		param.Created = types.NowDateTime()
	}
	if stored.Secret == "" {
		stored.Secret = security.RandomString(48)
		raw, _ := json.Marshal(stored)
		param.Value = types.JSONRaw(raw)
		param.Updated = types.NowDateTime()
		if err := s.app.Save(param); err != nil {
			s.app.Logger().Error("oauth-server: save key", "error", err)
		}
	}
	s.secret = []byte(stored.Secret)
	return s.secret
}

func (s *Server) sign(parts ...string) string {
	m := hmac.New(sha256.New, s.key())
	m.Write([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(m.Sum(nil))
}

func hashSecret(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// ScopeInfo is a scope as the consent page shows it.
type ScopeInfo struct {
	Name        string
	Description string
}

// resolveScopes checks a space-separated scope request against the registered scopes the owner collection may
// hold. Empty: full rights.
func (s *Server) resolveScopes(raw string, owner *core.Collection) ([]ScopeInfo, error) {
	registered := map[string]tokens.Scope{}
	for _, sc := range s.tokens().Scopes() {
		registered[sc.Name] = sc
	}
	var out []ScopeInfo
	seen := map[string]bool{}
	for _, name := range strings.Fields(raw) {
		name = strings.ToLower(name)
		if seen[name] || name == OfflineAccess {
			continue
		}
		seen[name] = true
		sc, ok := registered[name]
		if !ok {
			return nil, fmt.Errorf("unknown scope %q", name)
		}
		if owner != nil && len(sc.Collections) > 0 && !slices.Contains(sc.Collections, owner.Name) {
			return nil, fmt.Errorf("%s accounts can't grant the %q scope", owner.Name, name)
		}
		out = append(out, ScopeInfo{Name: sc.Name, Description: sc.Description})
	}
	return out, nil
}

func scopeNames(in []ScopeInfo) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Name)
	}
	return out
}

// limiter counts failed sign-ins per key (IP) in a window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) >= l.max
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.recent(key), time.Now())
	if len(l.hits) > 10000 {
		clear(l.hits)
	}
}

func (l *limiter) recent(key string) []time.Time {
	cut := time.Now().Add(-l.window)
	h := l.hits[key]
	i := 0
	for i < len(h) && h[i].Before(cut) {
		i++
	}
	return h[i:]
}
