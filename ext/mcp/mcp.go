// Package mcp serves a Model Context Protocol endpoint for one PocketBase app, so an agent (a Claude connector,
// Claude Code, any Streamable HTTP client) can manage the app and call the app's own tools.
//
//	app := pocketbase.New()
//	mcp.MustRegister(app, mcp.Config{})
//
// It comes with the built-in pb_* tools (collections, records, settings, logs, backups). Other plugins add tools with
// [Server.AddTool] on [For](app); an app's pb_hooks add them with $mcp.addTool (see [BindJS]).
//
// Sign-in is an [Authenticator]. The intended one is ext/oauth-server + ext/tokens (OAuth 2.1 the way Claude
// connectors expect: protected resource metadata, PKCE, CIMD/DCR, audience-bound tokens). Until that lands,
// [PocketBaseTokenAuth] accepts ordinary PocketBase auth tokens, which is fine for local use and tests.
package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

const (
	storeKey = "mcp"

	// DefaultPath is where the endpoint is served when [Config.Path] is empty.
	DefaultPath = "/mcp"

	// ScopeRead lets a client list and call tools marked read-only.
	ScopeRead = "mcp:read"
	// ScopeWrite lets a client call every tool it is allowed to see (it implies [ScopeRead]).
	ScopeWrite = "mcp:write"
)

// SupportedVersions are the MCP protocol revisions this server speaks, newest first.
var SupportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// Config is set by whoever builds the binary (or, under pocker, per instance).
type Config struct {
	// Path of the endpoint (default [DefaultPath]).
	Path string

	// Name is serverInfo.name (default: a slug of Settings → Application name, or "pocketbase").
	Name string
	// Version is serverInfo.version (default "dev").
	Version string
	// Description is serverInfo.description.
	Description string
	// Icons go to serverInfo.icons, e.g. [{"src": "https://…/logo.png", "mimeType": "image/png"}].
	Icons []map[string]any
	// Instructions are sent in initialize, before anything plugins and hooks add with [Server.AddInstructions].
	Instructions string

	// PublicURL is the origin clients reach the app at, used for the resource URL in protected resource metadata
	// (default: Settings → Application URL, else derived from the request).
	PublicURL string

	// Auth signs requests in (default [PocketBaseTokenAuth] with superusers only).
	Auth Authenticator

	// NoBuiltins leaves out the pb_* tools. BuiltinGroups limits them to some groups (see [BuiltinGroups]).
	NoBuiltins    bool
	BuiltinGroups []string
	// ReadOnlyBuiltins registers only the read-only pb_* tools.
	ReadOnlyBuiltins bool

	// AllowedOrigins lists browser origins (scheme://host[:port]) allowed besides the app's own host. Requests from
	// other origins get 403 (the spec's DNS rebinding guard). Server-to-server clients send no Origin and pass.
	AllowedOrigins []string
}

// Annotations are hints for clients (they treat them as untrusted unless they trust the server).
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// ReadOnly reports whether the tool only reads (it then needs just [ScopeRead]).
func (a *Annotations) ReadOnly() bool { return a != nil && a.ReadOnlyHint != nil && *a.ReadOnlyHint }

func ptr(b bool) *bool { return &b }

// Common annotation sets.
var (
	Read    = &Annotations{ReadOnlyHint: ptr(true)}
	Write   = &Annotations{ReadOnlyHint: ptr(false), DestructiveHint: ptr(false)}
	Destroy = &Annotations{ReadOnlyHint: ptr(false), DestructiveHint: ptr(true)}
)

// Tool is one callable tool.
type Tool struct {
	// Name: 1–128 characters of A-Za-z0-9_.- , unique per app.
	Name        string
	Title       string
	Description string
	// InputSchema is a JSON Schema object (default: no arguments).
	InputSchema map[string]any
	// OutputSchema, when set, describes structuredContent.
	OutputSchema map[string]any
	// Annotations; tools without ReadOnlyHint=true need [ScopeWrite].
	Annotations *Annotations

	// Allow decides who may see and call the tool (default [RequireSuperuser]).
	Allow func(c *Call) error

	// Handler runs the tool. Return any JSON-able value; return an error for a tool failure the model can read.
	Handler func(c *Call) (any, error)
}

// Call is passed to a tool's Allow and Handler.
type Call struct {
	App   core.App
	Event *core.RequestEvent
	Grant *Grant
	Tool  *Tool
	// Args are the tool arguments (never nil in Handler).
	Args map[string]any
}

// Auth is the signed-in record (may be nil for a grant without a record).
func (c *Call) Auth() *core.Record {
	if c.Grant == nil {
		return nil
	}
	return c.Grant.Auth
}

// RequireSuperuser allows only superusers.
func RequireSuperuser(c *Call) error {
	if a := c.Auth(); a != nil && a.IsSuperuser() {
		return nil
	}
	return errors.New("this tool requires a superuser")
}

// RequireAuth allows records of the given auth collections (any auth record when none are given).
func RequireAuth(collections ...string) func(c *Call) error {
	return func(c *Call) error {
		a := c.Auth()
		if a == nil {
			return errors.New("this tool requires sign-in")
		}
		if len(collections) == 0 || slices.Contains(collections, a.Collection().Name) || slices.Contains(collections, a.Collection().Id) {
			return nil
		}
		return fmt.Errorf("this tool is not available to %s", a.Collection().Name)
	}
}

// Server is the MCP service of one app. Get it with [For].
type Server struct {
	app core.App
	cfg Config

	mu           sync.RWMutex
	tools        map[string]*Tool
	instructions []string
}

var toolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, cfg Config) *Server {
	s, err := Register(app, cfg)
	if err != nil {
		panic(err)
	}
	return s
}

// Register adds the MCP endpoint and protected resource metadata to app (on serve) and the built-in tools. Call it
// before app.Start(), or from pocker's SetupApp. Plugins registered later add their tools through [For].
func Register(app core.App, cfg Config) (*Server, error) {
	if cfg.Path == "" {
		cfg.Path = DefaultPath
	}
	if !strings.HasPrefix(cfg.Path, "/") {
		cfg.Path = "/" + cfg.Path
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Auth == nil {
		cfg.Auth = PocketBaseTokenAuth{}
	}
	s := &Server{app: app, cfg: cfg, tools: map[string]*Tool{}}
	if !cfg.NoBuiltins {
		for _, t := range builtinTools(cfg.BuiltinGroups, cfg.ReadOnlyBuiltins) {
			if err := s.AddTool(t); err != nil {
				return nil, err
			}
		}
	}
	app.Store().Set(storeKey, s)

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		s.bindRoutes(se)
		return se.Next()
	})
	return s, nil
}

// For returns the MCP server registered on app, or nil.
func For(app core.App) *Server {
	s, _ := app.Store().Get(storeKey).(*Server)
	return s
}

// App returns the app this server belongs to.
func (s *Server) App() core.App { return s.app }

// Path returns the endpoint path.
func (s *Server) Path() string { return s.cfg.Path }

// AddTool registers t. It fails on an invalid or duplicate name, or a missing handler.
func (s *Server) AddTool(t Tool) error {
	if !toolName.MatchString(t.Name) {
		return fmt.Errorf("mcp: invalid tool name %q (1-128 of A-Za-z0-9_.-)", t.Name)
	}
	if t.Handler == nil {
		return fmt.Errorf("mcp: tool %q has no handler", t.Name)
	}
	if t.Allow == nil {
		t.Allow = RequireSuperuser
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tools[t.Name]; ok {
		return fmt.Errorf("mcp: tool %q is already registered", t.Name)
	}
	s.tools[t.Name] = &t
	return nil
}

// MustAddTool is [Server.AddTool] that panics on error.
func (s *Server) MustAddTool(t Tool) {
	if err := s.AddTool(t); err != nil {
		panic(err)
	}
}

// RemoveTool unregisters a tool (no-op when absent).
func (s *Server) RemoveTool(name string) {
	s.mu.Lock()
	delete(s.tools, name)
	s.mu.Unlock()
}

// Tools returns the registered tools sorted by name.
func (s *Server) Tools() []*Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Tool, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) tool(name string) *Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tools[name]
}

// AddInstructions appends a paragraph to the instructions sent in initialize (plugins describe their tools here).
func (s *Server) AddInstructions(text string) {
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	s.mu.Lock()
	s.instructions = append(s.instructions, text)
	s.mu.Unlock()
}

func (s *Server) instructionsText() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	parts := []string{}
	if t := strings.TrimSpace(s.cfg.Instructions); t != "" {
		parts = append(parts, t)
	} else {
		name := s.app.Settings().Meta.AppName
		if name == "" {
			name = "this PocketBase app"
		}
		parts = append(parts, "Tools for "+name+", a PocketBase app. pb_* tools manage the app itself (collections, records, settings, logs, backups); call pb_list_collections first to see its data model. Filters and sorts use PocketBase syntax.")
	}
	parts = append(parts, s.instructions...)
	return strings.Join(parts, "\n\n")
}

// visible reports whether c's grant may see t: the tool allows the caller, and the scopes cover it.
func visible(t *Tool, c *Call) bool {
	if t.Allow(c) != nil {
		return false
	}
	return c.Grant.has(ScopeWrite) || (t.Annotations.ReadOnly() && c.Grant.has(ScopeRead))
}

// publicOrigin is the scheme://host clients use to reach the app. The OAuth resource must match the URL the client
// actually uses, so the request's host wins over Settings → Application URL (which defaults to localhost:8090);
// the setting only supplies the scheme when its host is the request's.
func (s *Server) publicOrigin(e *core.RequestEvent) string {
	if u := strings.TrimRight(s.cfg.PublicURL, "/"); u != "" {
		return u
	}
	host := e.Request.Host
	if app, err := url.Parse(s.app.Settings().Meta.AppURL); err == nil && app.Host != "" && strings.EqualFold(app.Host, host) {
		return app.Scheme + "://" + app.Host
	}
	scheme := "http"
	if e.Request.TLS != nil || strings.EqualFold(e.Request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + host
}

// ResourceURL is the canonical URL of the endpoint (the OAuth resource / token audience).
func (s *Server) ResourceURL(e *core.RequestEvent) string { return s.publicOrigin(e) + s.cfg.Path }

func methodNotAllowed(e *core.RequestEvent) error {
	e.Response.Header().Set("Allow", http.MethodPost)
	return e.JSON(http.StatusMethodNotAllowed, map[string]any{"message": "Use POST."})
}
