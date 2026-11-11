package pages

import (
	"ganigeorgiev.com/pocketbase/pocketbase/tools/subscriptions"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, dir, path string, dev bool) (int, string) {
	t.Helper()
	h, p := newServer(t, dir)
	p.cfg.Dev = &dev
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(b)
}

func TestEJSTags(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.ejs": "a\n  <%_ if (true) { _%>\nb\n<%_ } _%>\nc <%= '%%>' %> <%# x %>|<%- 1 + 1 %>|\n<% var s = 'x' %><%= s; %>",
	})
	code, body := get(t, dir, "/", false)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	want := "a\nb\nc %&gt; |2|\nx"
	if body != want {
		t.Fatalf("got %q, want %q", body, want)
	}
}

func TestDevErrorNamesTemplateLine(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.ejs": "line1\nline2\n<%= nope.x %>\n"})
	code, body := get(t, dir, "/", true)
	if code != 500 || !strings.Contains(body, "index.ejs:3") || !strings.Contains(body, "nope") {
		t.Fatalf("%d %s", code, body)
	}
	code, body = get(t, dir, "/", false)
	if code != 500 || strings.Contains(body, "nope") || !strings.Contains(body, "Something went wrong") {
		t.Fatalf("prod error page leaks details: %d %s", code, body)
	}
}

func TestIncludeRecursionGuard(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.ejs":         "<%- include('loop') %>",
		"_private/loop.ejs": "x<%- include('loop') %>",
	})
	code, body := get(t, dir, "/", true)
	if code != 500 || !strings.Contains(body, "nested more than") {
		t.Fatalf("%d %.300s", code, body)
	}
}

func TestIncludeCantEscape(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.ejs": "<%- include('../../etc/passwd') %>"})
	code, body := get(t, dir, "/", true)
	if code != 500 || !strings.Contains(body, "invalid name") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestMiddlewareMustCallNext(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+middleware.js": "module.exports = (ctx, next) => { ctx.data.x = 1 }",
		"index.ejs":      "hi",
	})
	code, body := get(t, dir, "/", true)
	if code != 500 || !strings.Contains(body, "without calling next()") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestModuleRequireRelative(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+page.js":           "const h = require('./_private/helper'); module.exports = { load(ctx) { return { v: h.twice(21) } } }",
		"_private/helper.js": "exports.twice = (n) => n * 2",
		"index.ejs":          "<%= data.v %>",
	})
	code, body := get(t, dir, "/", true)
	if code != 200 || body != "42" {
		t.Fatalf("%d %s", code, body)
	}
}

func TestStarterKitsRender(t *testing.T) {
	dirs, _ := filepath.Glob("starters/*/pb_pages")
	dirs = append(dirs, "site/pb_pages")
	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			table, errs := Build(dir)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			h, p := newServer(t, dir)
			dev := true
			p.cfg.Dev = &dev
			for _, r := range table.Routes {
				if r.Kind != KindPage || strings.Contains(r.Pattern, "[") {
					continue
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest("GET", r.Pattern, nil))
				if rec.Code >= 500 { // redirects and 4xx from the kit's own logic are fine
					b, _ := io.ReadAll(rec.Result().Body)
					t.Errorf("%s (%s): %d %.400s", r.Pattern, r.File, rec.Code, b)
				}
			}
		})
	}
}

func TestAddonCopiesMatch(t *testing.T) {
	want, err := os.ReadFile("addons/datastar.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, copy := range []string{"starters/datastar/pb_pages/_private/datastar.js", "site/pb_pages/_private/datastar.js"} {
		got, err := os.ReadFile(copy)
		if err != nil || string(got) != string(want) {
			t.Errorf("%s differs from addons/datastar.js (copy it again)", copy)
		}
	}
}

func TestSSE(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"stream/+api.js": "module.exports = { get(ctx) { const s = ctx.res.sse(); s.send('hello', 'a\\nb', { id: 7 }); s.send('obj', { x: 1 }); s.comment('ping') } }",
	})
	h, _ := newServer(t, dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/stream", nil))
	want := "id: 7\nevent: hello\ndata: a\ndata: b\n\nevent: obj\ndata: {\"x\":1}\n\n: ping\n\n"
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" || rec.Body.String() != want {
		t.Fatalf("%d %q\n%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestDatastarRequestGetsAPatch(t *testing.T) {
	addon, _ := os.ReadFile("addons/datastar.js")
	dir := writeTree(t, map[string]string{
		"+config.js":           "module.exports = { plugins: [require('./_private/datastar.js')] }",
		"_private/datastar.js": string(addon),
		"+layout.ejs":          "<html><%- slot() %></html>",
		"index.ejs":            "<div id=\"x\">\nhi\n</div>",
		"sig/+api.js":          "module.exports = { get(ctx) { const s = ctx.datastar.signals(); ctx.datastar.patchSignals({ seen: s.n }) } }",
	})
	h, _ := newServer(t, dir)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Datastar-Selector", "#main")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	want := "event: datastar-patch-elements\ndata: selector #main\ndata: mode inner\ndata: elements <div id=\"x\">\ndata: elements hi\ndata: elements </div>\n\n"
	if rec.Body.String() != want {
		t.Fatalf("%q", rec.Body.String())
	}
	// a normal request still gets the page with its layout
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), "<html><div id=\"x\">") {
		t.Fatalf("%q", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/sig?datastar=%7B%22n%22%3A5%7D", nil))
	if !strings.Contains(rec.Body.String(), "data: signals {\"seen\":5}") {
		t.Fatalf("%q", rec.Body.String())
	}
}

func TestAuthSession(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+config.js": "module.exports = { plugins: ['auth'] }",
		"login/+api.js": `module.exports = { post(ctx) {
			const b = ctx.req.body()
			try { ctx.session.signInWithPassword(b.email, b.password) } catch (e) { return ctx.res.json(401, { error: String(e) }) }
			return ctx.res.json({ id: ctx.auth.id, email: ctx.auth.email() })
		} }`,
		"me/+api.js":      "module.exports = { get(ctx) { return ctx.res.json({ id: ctx.auth ? ctx.auth.id : null }) } }",
		"logout/+api.js":  "module.exports = { post(ctx) { ctx.session.signOut(); return ctx.res.json({ id: ctx.auth }) } }",
		"methods/+api.js": "module.exports = { get(ctx) { return ctx.res.json(ctx.session.authMethods()) } }",
	})
	h, _ := newServer(t, dir)
	do := func(method, path, body, cookie string) *httptest.ResponseRecorder {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rd)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	// tests.NewTestApp's users: test@example.com / 1234567890
	rec := do("POST", "/login", `{"email":"test@example.com","password":"wrong"}`, "")
	if rec.Code != 401 {
		t.Fatalf("bad password: %d %s", rec.Code, rec.Body.String())
	}
	rec = do("POST", "/login", `{"email":"test@example.com","password":"1234567890"}`, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "test@example.com") {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "pb_auth" {
			cookie = "pb_auth=" + c.Value
			if !c.HttpOnly {
				t.Error("pb_auth must be HttpOnly")
			}
		}
	}
	if cookie == "" {
		t.Fatal("no pb_auth cookie")
	}
	if rec := do("GET", "/me", "", cookie); !strings.Contains(rec.Body.String(), `"id":"`) || strings.Contains(rec.Body.String(), `"id":null`) {
		t.Fatalf("cookie session not read: %s", rec.Body.String())
	}
	if rec := do("GET", "/me", "", "pb_auth=garbage"); !strings.Contains(rec.Body.String(), `"id":null`) || !strings.Contains(strings.Join(rec.Header().Values("Set-Cookie"), ";"), "Max-Age=0") {
		t.Fatalf("bad cookie should be cleared: %s %v", rec.Body.String(), rec.Header().Values("Set-Cookie"))
	}
	if rec := do("POST", "/logout", "", cookie); !strings.Contains(rec.Body.String(), `"id":null`) {
		t.Fatalf("logout: %s", rec.Body.String())
	}
	if rec := do("GET", "/methods", "", ""); !strings.Contains(rec.Body.String(), `"password":{"enabled":true`) {
		t.Fatalf("authMethods: %s", rec.Body.String())
	}
}

func TestRealtimeSend(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+config.js":   "module.exports = { plugins: ['realtime'] }",
		"send/+api.js": "module.exports = { post(ctx) { ctx.realtime.send('chat', '<p>hi</p>', { filter: (c) => c.get('room') === 'a' }); return ctx.res.json({ ok: true }) } }",
	})
	h, p := newServer(t, dir)
	broker := p.app.SubscriptionsBroker()
	inRoom := subscriptions.NewDefaultClient()
	inRoom.Subscribe("chat")
	inRoom.Set("room", "a")
	otherRoom := subscriptions.NewDefaultClient()
	otherRoom.Subscribe("chat")
	otherRoom.Set("room", "b")
	notSubscribed := subscriptions.NewDefaultClient()
	notSubscribed.Set("room", "a")
	for _, c := range []subscriptions.Client{inRoom, otherRoom, notSubscribed} {
		broker.Register(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/send", nil))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	select {
	case m := <-inRoom.Channel():
		if m.Name != "chat" || string(m.Data) != `"<p>hi</p>"` {
			t.Fatalf("got %s %s", m.Name, m.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribed client in the room got nothing")
	}
	for name, c := range map[string]subscriptions.Client{"other room": otherRoom, "not subscribed": notSubscribed} {
		select {
		case m := <-c.Channel():
			t.Errorf("%s got %s", name, m.Data)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func TestJSSDKAppliesAPIRules(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+config.js": "module.exports = { plugins: ['auth'] }",
		"check/+api.js": `module.exports = { get(ctx) {
			const guest = ctx.pb().collection('users').getList(1, 50)
			ctx.session.signInWithPassword('test@example.com', '1234567890')
			const mine = ctx.pb().collection('users').getList(1, 50)
			let missing = 0
			try { ctx.pb().collection('users').getOne('nope') } catch (e) { missing = e.status }
			return ctx.res.json({ guest: guest.totalItems, mine: mine.totalItems, email: mine.items[0].email, missing })
		} }`,
	})
	h, p := newServer(t, dir)
	dev := true
	p.cfg.Dev = &dev
	users, err := p.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	rule := "id = @request.auth.id"
	users.ListRule, users.ViewRule = &rule, &rule
	if err := p.app.Save(users); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/check", nil))
	body := rec.Body.String()
	// a user sees only their own record; guests see none
	if rec.Code != 200 || !strings.Contains(body, `"guest":0`) || !strings.Contains(body, `"mine":1`) ||
		!strings.Contains(body, `"email":"test@example.com"`) || !strings.Contains(body, `"missing":404`) {
		t.Fatalf("%d %s", rec.Code, body)
	}
}

func TestNotFoundPage(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+layout.ejs":     "<main><%- slot() %></main>",
		"+middleware.js":  "module.exports = (ctx, next) => { ctx.data.mw = 'yes'; return next() }",
		"+404.ejs":        "missing <%= ctx.req.url.pathname %> mw=<%= data.mw %>",
		"index.ejs":       "home",
		"sub/+layout.ejs": "<sub><%- slot() %></sub>",
	})
	h, _ := newServer(t, dir)
	do := func(method, path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		b, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(b)
	}
	code, body := do("GET", "/sub/nope")
	if code != 404 || !strings.HasPrefix(body, "<main>missing /sub/nope mw=yes") || strings.Contains(body, "<sub>") {
		t.Fatalf("GET: %d %q", code, body)
	}
	if code, _ := do("HEAD", "/nope"); code != 404 {
		t.Fatalf("HEAD: %d", code)
	}
	if code, body := do("GET", "/"); code != 200 || body != "<main>home</main>" {
		t.Fatalf("home: %d %q", code, body)
	}
	if code, body := do("POST", "/nope"); code != 404 || strings.Contains(body, "missing") {
		t.Fatalf("POST: %d %q", code, body)
	}
	if code, body := do("GET", "/api/nope"); code != 404 || strings.Contains(body, "missing") {
		t.Fatalf("api: %d %q", code, body)
	}
}

func TestMarkdownImagesUseAsset(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"docs/guide.md":     "![a](img/shot.png) ![b](https://x.test/b.png) ![c](missing.png) ![d](/abs.png)",
		"docs/img/shot.png": "png",
		"abs.png":           "png",
	})
	code, body := get(t, dir, "/docs/guide", false)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, want := range []string{`src="/docs/img/shot.`, `src="https://x.test/b.png"`, `src="missing.png"`, `src="/abs.png"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("want %s in %s", want, body)
		}
	}
	if strings.Contains(body, `src="img/shot.png"`) {
		t.Fatalf("relative image not fingerprinted: %s", body)
	}
}
