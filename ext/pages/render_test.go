package pages

import (
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
	// kits that need built-in plugins (auth, realtime, datastar) are checked once those land
	for _, kit := range []string{"minimal", "mvp", "daisyui", "daisyui-docs"} {
		t.Run(kit, func(t *testing.T) {
			dir := filepath.Join("starters", kit, "pb_pages")
			table, errs := Build(dir)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			h, p := newServer(t, dir)
			dev := true
			p.cfg.Dev = &dev
			for _, r := range table.Routes {
				if r.Kind != KindPage || len(r.Segments) > 0 && r.Segments[len(r.Segments)-1].Param != "" {
					continue
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest("GET", r.Pattern, nil))
				if rec.Code != 200 {
					b, _ := io.ReadAll(rec.Result().Body)
					t.Errorf("%s (%s): %d %.400s", r.Pattern, r.File, rec.Code, b)
				}
			}
		})
	}
}
