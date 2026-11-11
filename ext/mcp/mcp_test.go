package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/dop251/goja"
)

type harness struct {
	t       *testing.T
	app     *tests.TestApp
	srv     *Server
	handler http.Handler
}

func newHarness(t *testing.T, cfg Config, setup ...func(*Server)) *harness {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	s := MustRegister(app, cfg)
	for _, f := range setup {
		f(s)
	}
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, app: app, srv: s}
	ev := &core.ServeEvent{App: app, Router: r}
	if err := app.OnServe().Trigger(ev, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		h.handler = mux
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) token(collection, email string) string {
	h.t.Helper()
	rec, err := h.app.FindAuthRecordByEmail(collection, email)
	if err != nil {
		h.t.Fatal(err)
	}
	tok, err := rec.NewAuthToken()
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func (h *harness) superuser() string {
	return h.token(core.CollectionNameSuperusers, "test@example.com")
}

type resp struct {
	code   int
	header http.Header
	body   map[string]any
	raw    string
}

func (h *harness) do(method, path, token, body string, headers ...string) resp {
	h.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	r := resp{code: rec.Code, header: rec.Header(), raw: rec.Body.String()}
	_ = json.Unmarshal(rec.Body.Bytes(), &r.body)
	return r
}

// rpc posts one JSON-RPC request and returns the response.
func (h *harness) rpc(token, method string, params any) resp {
	h.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return h.do(http.MethodPost, "/mcp", token, string(b))
}

func (h *harness) call(token, tool string, args map[string]any) map[string]any {
	h.t.Helper()
	r := h.rpc(token, "tools/call", map[string]any{"name": tool, "arguments": args})
	if r.code != 200 {
		h.t.Fatalf("%s: HTTP %d %s", tool, r.code, r.raw)
	}
	res, ok := r.body["result"].(map[string]any)
	if !ok {
		h.t.Fatalf("%s: no result: %s", tool, r.raw)
	}
	return res
}

func (h *harness) callOK(token, tool string, args map[string]any) map[string]any {
	h.t.Helper()
	res := h.call(token, tool, args)
	if res["isError"] == true {
		h.t.Fatalf("%s failed: %v", tool, res["content"])
	}
	sc, _ := res["structuredContent"].(map[string]any)
	return sc
}

func toolNames(r resp) []string {
	names := []string{}
	res, _ := r.body["result"].(map[string]any)
	list, _ := res["tools"].([]any)
	for _, t := range list {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	return names
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestChallengeAndMetadata(t *testing.T) {
	h := newHarness(t, Config{PublicURL: "https://acme.example.com"})

	r := h.rpc("", "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if r.code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d %s", r.code, r.raw)
	}
	wa := r.header.Get("WWW-Authenticate")
	if !strings.Contains(wa, `resource_metadata="https://acme.example.com/.well-known/oauth-protected-resource/mcp"`) ||
		!strings.Contains(wa, `scope="mcp:read mcp:write"`) || strings.Contains(wa, "invalid_token") {
		t.Fatalf("bad challenge: %s", wa)
	}

	r = h.rpc("not-a-token", "initialize", nil)
	if r.code != http.StatusUnauthorized || !strings.Contains(r.header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("bad token: want 401 invalid_token, got %d %s", r.code, r.header.Get("WWW-Authenticate"))
	}

	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		r = h.do(http.MethodGet, path, "", "")
		if r.code != 200 || r.body["resource"] != "https://acme.example.com/mcp" {
			t.Fatalf("%s: %d %s", path, r.code, r.raw)
		}
		as, _ := r.body["authorization_servers"].([]any)
		if len(as) != 1 || as[0] != "https://acme.example.com" {
			t.Fatalf("%s: authorization_servers %v", path, as)
		}
	}

	// without PublicURL the resource follows the host the client used, not the default app URL
	h2 := newHarness(t, Config{})
	req := h2.do(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", "", "X-Forwarded-Proto", "https")
	if req.body["resource"] != "https://example.com/mcp" {
		t.Fatalf("derived resource: %s", req.raw)
	}

	if r = h.do(http.MethodGet, "/mcp", h.superuser(), ""); r.code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: want 405, got %d", r.code)
	}
}

func TestProtocol(t *testing.T) {
	h := newHarness(t, Config{Name: "acme", Version: "1.2.3", Instructions: "Be nice."}, func(s *Server) {
		s.AddInstructions("Extra notes.")
	})
	tok := h.superuser()

	r := h.rpc(tok, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	res := r.body["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" {
		t.Fatalf("version not echoed: %v", res["protocolVersion"])
	}
	info := res["serverInfo"].(map[string]any)
	if info["name"] != "acme" || info["version"] != "1.2.3" || info["title"] == "" {
		t.Fatalf("serverInfo: %v", info)
	}
	if res["instructions"] != "Be nice.\n\nExtra notes." {
		t.Fatalf("instructions: %q", res["instructions"])
	}

	r = h.rpc(tok, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	if v := r.body["result"].(map[string]any)["protocolVersion"]; v != SupportedVersions[0] {
		t.Fatalf("unknown version: want newest, got %v", v)
	}

	if r = h.rpc(tok, "ping", nil); r.code != 200 || r.body["result"] == nil {
		t.Fatalf("ping: %s", r.raw)
	}

	if r = h.do(http.MethodPost, "/mcp", tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.code != http.StatusAccepted {
		t.Fatalf("notification: want 202, got %d", r.code)
	}
	if r = h.do(http.MethodPost, "/mcp", tok, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`); r.code != http.StatusBadRequest {
		t.Fatalf("batch: want 400, got %d", r.code)
	}
	if r = h.do(http.MethodPost, "/mcp", tok, `{nope`); r.code != http.StatusBadRequest {
		t.Fatalf("parse error: want 400, got %d", r.code)
	}
	if r = h.do(http.MethodPost, "/mcp", tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "MCP-Protocol-Version", "1999-01-01"); r.code != http.StatusBadRequest {
		t.Fatalf("bad protocol header: want 400, got %d", r.code)
	}
	if r = h.do(http.MethodPost, "/mcp", tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "MCP-Protocol-Version", "2025-11-25"); r.code != 200 {
		t.Fatalf("good protocol header: want 200, got %d", r.code)
	}
	if r = h.do(http.MethodPost, "/mcp", tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example"); r.code != http.StatusForbidden {
		t.Fatalf("foreign origin: want 403, got %d", r.code)
	}
	r = h.do(http.MethodPost, "/mcp", tok, `{"jsonrpc":"2.0","id":"abc","method":"resources/list"}`)
	if e, _ := r.body["error"].(map[string]any); e == nil || e["code"] != float64(codeMethodNotFound) || r.body["id"] != "abc" {
		t.Fatalf("unknown method: %s", r.raw)
	}

	r = h.rpc(tok, "tools/call", map[string]any{"name": "nope"})
	if e, _ := r.body["error"].(map[string]any); e == nil || e["code"] != float64(codeInvalidParams) {
		t.Fatalf("unknown tool: %s", r.raw)
	}
}

func TestBuiltins(t *testing.T) {
	h := newHarness(t, Config{})
	tok := h.superuser()

	names := toolNames(h.rpc(tok, "tools/list", nil))
	for _, want := range []string{"pb_server_info", "pb_list_collections", "pb_create_record", "pb_delete_collection", "pb_create_backup"} {
		if !contains(names, want) {
			t.Fatalf("missing %s in %v", want, names)
		}
	}

	info := h.callOK(tok, "pb_server_info", nil)
	if a, _ := info["auth"].(map[string]any); a["collection"] != core.CollectionNameSuperusers {
		t.Fatalf("server info auth: %v", info)
	}

	h.callOK(tok, "pb_create_collection", map[string]any{"collection": map[string]any{
		"name": "mcp_posts", "type": "base",
		"fields": []any{map[string]any{"name": "title", "type": "text", "required": true}},
	}})
	cols := h.callOK(tok, "pb_list_collections", nil)["collections"].([]any)
	found := false
	for _, c := range cols {
		if c.(map[string]any)["name"] == "mcp_posts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mcp_posts not listed")
	}

	rec := h.callOK(tok, "pb_create_record", map[string]any{"collection": "mcp_posts", "data": map[string]any{"title": "hello"}})
	id, _ := rec["id"].(string)
	if id == "" {
		t.Fatalf("create record: %v", rec)
	}
	h.callOK(tok, "pb_update_record", map[string]any{"collection": "mcp_posts", "id": id, "data": map[string]any{"title": "hi"}})
	if got := h.callOK(tok, "pb_get_record", map[string]any{"collection": "mcp_posts", "id": id}); got["title"] != "hi" {
		t.Fatalf("get record: %v", got)
	}
	list := h.callOK(tok, "pb_list_records", map[string]any{"collection": "mcp_posts", "filter": "title = {:t}", "params": map[string]any{"t": "hi"}})
	if items := list["items"].([]any); len(items) != 1 || list["hasMore"] != false {
		t.Fatalf("list records: %v", list)
	}
	h.callOK(tok, "pb_delete_record", map[string]any{"collection": "mcp_posts", "id": id})

	// missing required field -> tool error, not a protocol error
	if res := h.call(tok, "pb_create_record", map[string]any{"collection": "mcp_posts", "data": map[string]any{}}); res["isError"] != true {
		t.Fatalf("validation error should be isError: %v", res)
	}
	if res := h.call(tok, "pb_delete_collection", map[string]any{"collection": "mcp_posts", "confirm": "wrong"}); res["isError"] != true {
		t.Fatalf("bad confirm should fail: %v", res)
	}
	h.callOK(tok, "pb_delete_collection", map[string]any{"collection": "mcp_posts", "confirm": "mcp_posts"})

	h.callOK(tok, "pb_update_settings", map[string]any{"changes": map[string]any{"meta": map[string]any{"appName": "Acme MCP"}}})
	if h.app.Settings().Meta.AppName != "Acme MCP" {
		t.Fatalf("settings not updated")
	}
	s := h.callOK(tok, "pb_get_settings", nil)
	if smtp, _ := s["smtp"].(map[string]any); smtp["password"] != nil && smtp["password"] != "" {
		t.Fatalf("smtp password leaked: %v", smtp["password"])
	}
	h.callOK(tok, "pb_list_logs", map[string]any{"limit": 5})
	h.callOK(tok, "pb_list_backups", nil)

	// a regular user's token isn't accepted by the default auth
	if r := h.rpc(h.token("users", "test@example.com"), "tools/list", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("user token: want 401, got %d", r.code)
	}
}

func TestReadOnlyBuiltinsAndGroups(t *testing.T) {
	h := newHarness(t, Config{BuiltinGroups: []string{"records"}, ReadOnlyBuiltins: true})
	names := toolNames(h.rpc(h.superuser(), "tools/list", nil))
	if len(names) != 2 || !contains(names, "pb_list_records") || !contains(names, "pb_get_record") {
		t.Fatalf("read-only records tools: %v", names)
	}
}

// scopedAuth grants a fixed scope set to any superuser token, standing in for ext/oauth-server.
type scopedAuth struct{ scopes []string }

func (a scopedAuth) Authenticate(e *core.RequestEvent, resource string) (*Grant, error) {
	g, err := PocketBaseTokenAuth{}.Authenticate(e, resource)
	if err != nil {
		return nil, err
	}
	g.Scopes, g.ClientID = a.scopes, "test-client"
	return g, nil
}

func (a scopedAuth) AuthorizationServers(e *core.RequestEvent, s *Server) []string {
	return []string{"https://issuer.example"}
}

func TestScopes(t *testing.T) {
	h := newHarness(t, Config{Auth: scopedAuth{scopes: []string{ScopeRead}}})
	tok := h.superuser()

	names := toolNames(h.rpc(tok, "tools/list", nil))
	if !contains(names, "pb_list_records") || contains(names, "pb_create_record") {
		t.Fatalf("read scope should hide write tools: %v", names)
	}
	r := h.rpc(tok, "tools/call", map[string]any{"name": "pb_create_record", "arguments": map[string]any{}})
	if r.code != http.StatusForbidden || !strings.Contains(r.header.Get("WWW-Authenticate"), `error="insufficient_scope", scope="mcp:write"`) {
		t.Fatalf("write tool with read scope: want 403 insufficient_scope, got %d %s", r.code, r.header.Get("WWW-Authenticate"))
	}
	if info := h.callOK(tok, "pb_server_info", nil); info["scopes"].([]any)[0] != ScopeRead {
		t.Fatalf("scopes in server info: %v", info)
	}
	if r := h.do(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", ""); r.body["authorization_servers"].([]any)[0] != "https://issuer.example" {
		t.Fatalf("authenticator's issuer not in metadata: %s", r.raw)
	}
}

func TestPluginTools(t *testing.T) {
	h := newHarness(t, Config{Auth: PocketBaseTokenAuth{Collections: []string{core.CollectionNameSuperusers, "users"}}}, func(s *Server) {
		s.MustAddTool(Tool{
			Name:        "whoami",
			Description: "The signed-in user's email.",
			Annotations: Read,
			Allow:       RequireAuth("users"),
			Handler:     func(c *Call) (any, error) { return map[string]any{"email": c.Auth().Email()}, nil },
		})
		s.MustAddTool(Tool{
			Name:    "boom",
			Handler: func(c *Call) (any, error) { panic("kaboom") },
		})
		s.MustAddTool(Tool{
			Name:    "fails",
			Handler: func(c *Call) (any, error) { return nil, errors.New("nope") },
		})
		s.MustAddTool(Tool{Name: "text", Handler: func(c *Call) (any, error) { return "plain", nil }})
		if err := s.AddTool(Tool{Name: "whoami", Handler: func(c *Call) (any, error) { return nil, nil }}); err == nil {
			t.Fatal("duplicate tool name accepted")
		}
		if err := s.AddTool(Tool{Name: "bad name", Handler: func(c *Call) (any, error) { return nil, nil }}); err == nil {
			t.Fatal("invalid tool name accepted")
		}
	})

	user := h.token("users", "test@example.com")
	names := toolNames(h.rpc(user, "tools/list", nil))
	if len(names) != 1 || names[0] != "whoami" {
		t.Fatalf("a user should see only whoami: %v", names)
	}
	if got := h.callOK(user, "whoami", nil); got["email"] != "test@example.com" {
		t.Fatalf("whoami: %v", got)
	}
	// a user can't call superuser tools, and can't learn they exist
	r := h.rpc(user, "tools/call", map[string]any{"name": "pb_list_collections"})
	if e, _ := r.body["error"].(map[string]any); e == nil || e["message"] != "Unknown tool: pb_list_collections" {
		t.Fatalf("user calling a superuser tool: %s", r.raw)
	}

	su := h.superuser()
	if contains(toolNames(h.rpc(su, "tools/list", nil)), "whoami") {
		t.Fatal("superuser shouldn't see a users-only tool")
	}
	if res := h.call(su, "boom", nil); res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "kaboom") {
		t.Fatalf("panic should be a tool error: %v", res)
	}
	if res := h.call(su, "fails", nil); res["isError"] != true {
		t.Fatalf("error should be a tool error: %v", res)
	}
	if res := h.call(su, "text", nil); res["structuredContent"] != nil || res["content"].([]any)[0].(map[string]any)["text"] != "plain" {
		t.Fatalf("string result: %v", res)
	}
}

func TestBindJS(t *testing.T) {
	h := newHarness(t, Config{Auth: PocketBaseTokenAuth{Collections: []string{core.CollectionNameSuperusers, "users"}}})

	vm := goja.New()
	jsvm.BindCore(vm) // as in pb_hooks: Go methods are lowerCamelCase
	BindJS(vm, h.app)
	_, err := vm.RunString(`
		const greeting = "hello" // top-level variables are visible to handlers
		$mcp.addTool({
			name: "greet",
			title: "Greet",
			description: "Say hello.",
			inputSchema: { type: "object", properties: { name: { type: "string" } }, required: ["name"] },
			annotations: { readOnlyHint: true },
			allow: "auth",
			handler: ({ args, auth }) => ({ text: greeting + " " + args.name, by: auth.email() }),
		})
		$mcp.addTool({
			name: "js_fail",
			description: "Always throws.",
			handler: () => { throw new Error("js said no") },
		})
		$mcp.instructions("greet greets.")
		if (!$mcp.tools().includes("greet")) throw new Error("greet not listed")
	`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(`$mcp.addTool({ name: "x" })`); err == nil {
		t.Fatal("addTool without handler should throw")
	}

	user := h.token("users", "test@example.com")
	if got := h.callOK(user, "greet", map[string]any{"name": "Ben"}); got["text"] != "hello Ben" || got["by"] != "test@example.com" {
		t.Fatalf("greet: %v", got)
	}
	if res := h.call(h.superuser(), "js_fail", nil); res["isError"] != true || res["content"].([]any)[0].(map[string]any)["text"] != "js said no" {
		t.Fatalf("js_fail: %v", res)
	}
	r := h.rpc(user, "initialize", nil)
	if !strings.Contains(r.body["result"].(map[string]any)["instructions"].(string), "greet greets.") {
		t.Fatalf("JS instructions missing: %s", r.raw)
	}
}
