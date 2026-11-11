package pages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCoreFixtureTable(t *testing.T) {
	table, errs := Build("fixtures/core/pb_pages")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := map[string]struct {
		kind              Kind
		pattern, file     string
		handler           string
		middlewares       int
		layouts           []string
		params            map[string]string
		fingerprintedPath bool
	}{
		"/":               {kind: KindPage, pattern: "/", file: "index.ejs", middlewares: 1, layouts: []string{"+layout.ejs"}},
		"/about/":         {kind: KindPage, pattern: "/about", file: "about.ejs", middlewares: 1, layouts: []string{"+layout.ejs"}},
		"/pricing":        {kind: KindPage, pattern: "/pricing", file: "(marketing)/pricing.ejs", middlewares: 1, layouts: []string{"+layout.ejs"}},
		"/docs":           {kind: KindPage, pattern: "/docs", file: "docs/index.md", middlewares: 1, layouts: []string{"docs/+layout.ejs", "+layout.ejs"}},
		"/products/new":   {kind: KindPage, pattern: "/products/new", file: "products/new/index.ejs", middlewares: 1, layouts: []string{"+layout.ejs"}},
		"/products/42":    {kind: KindPage, pattern: "/products/[id]", file: "products/[id]/index.ejs", handler: "products/[id]/+page.js", middlewares: 1, layouts: []string{"+layout.ejs"}, params: map[string]string{"id": "42"}},
		"/repos/acme/web": {kind: KindPage, pattern: "/repos/[org]/[repo]", file: "repos/[org]/[repo]/index.ejs", middlewares: 1, layouts: []string{"+layout.ejs"}, params: map[string]string{"org": "acme", "repo": "web"}},
		"/admin":          {kind: KindPage, pattern: "/admin", file: "admin/index.ejs", middlewares: 2, layouts: []string{"+layout.ejs"}},
		"/feeds/latest":   {kind: KindAPI, pattern: "/feeds/latest", file: "feeds/latest/+api.js", handler: "feeds/latest/+api.js", middlewares: 1},
		"/assets/app.css": {kind: KindStatic, pattern: "/assets/app.css", file: "assets/app.css"},
		"/assets":         {kind: KindPage, pattern: "/assets", file: "assets.ejs", middlewares: 1, layouts: []string{"+layout.ejs"}},
	}
	for url, w := range want {
		r, params, _ := table.Match(url, nil)
		if r == nil {
			t.Errorf("%s: no match", url)
			continue
		}
		if r.Kind != w.kind || r.Pattern != w.pattern || r.File != w.file || r.Handler != w.handler {
			t.Errorf("%s: got %s %s %s handler=%q", url, r.Kind, r.Pattern, r.File, r.Handler)
		}
		if len(r.Middlewares) != w.middlewares {
			t.Errorf("%s: middlewares %v", url, r.Middlewares)
		}
		if strings.Join(r.Layouts, ",") != strings.Join(w.layouts, ",") {
			t.Errorf("%s: layouts %v, want %v", url, r.Layouts, w.layouts)
		}
		for k, v := range w.params {
			if params[k] != v {
				t.Errorf("%s: param %s = %q, want %q", url, k, params[k], v)
			}
		}
	}
	for _, url := range []string{"/(marketing)/pricing", "/-drafts/secret", "/_private/nav", "/+layout", "/nothing", "/products/42/extra", "/products/"} {
		if r, _, _ := table.Match(url, nil); r != nil {
			t.Errorf("%s should not match, got %s", url, r.File)
		}
	}
	if table.Config != "+config.js" || table.ErrorTpl != "+error.ejs" {
		t.Errorf("root files: %q %q", table.Config, table.ErrorTpl)
	}
	r, _, _ := table.Match("/admin", nil)
	if r.Middlewares[0] != "+middleware.js" || r.Middlewares[1] != "admin/+middleware.js" {
		t.Errorf("middleware order: %v", r.Middlewares)
	}
}

func TestBuildErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"api folder":        {"api/x.ejs": ""},
		"partial param":     {"post-[id].ejs": ""},
		"duplicate url":     {"about.ejs": "", "about/index.ejs": ""},
		"same params":       {"p/[id].ejs": "", "p/[slug].ejs": ""},
		"api and index":     {"f/+api.js": "", "f/index.ejs": ""},
		"unknown plus file": {"+foo.js": ""},
		"root-only file":    {"sub/+config.js": ""},
		"static vs page":    {"x.ejs": "", "x": "static"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := Build(writeTree(t, files))
			if len(errs) == 0 {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestFingerprint(t *testing.T) {
	if got := fingerprint("app.css", "0123456789ab"); got != "app.0123456789ab.css" {
		t.Fatal(got)
	}
	if got := fingerprint("LICENSE", "0123456789ab"); got != "LICENSE.0123456789ab" {
		t.Fatal(got)
	}
	if !looksFingerprinted("app.0123456789ab.css") || looksFingerprinted("app.css") {
		t.Fatal("looksFingerprinted")
	}
}

func TestKitsAndSiteBuild(t *testing.T) {
	dirs, _ := filepath.Glob("starters/*/pb_pages")
	dirs = append(dirs, "site/pb_pages")
	for _, d := range dirs {
		t.Run(d, func(t *testing.T) {
			table, errs := Build(d)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if len(table.Routes) == 0 {
				t.Fatal("no routes")
			}
		})
	}
}

func TestRescanPicksUpNewFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.ejs": "a"})
	h, p := newServer(t, dir)
	_ = h
	p.cfg.RescanInterval = 1 // effectively every request
	if r, _, _ := p.Table().Match("/b.txt", nil); r != nil {
		t.Fatal("b.txt shouldn't exist yet")
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := p.Table().Match("/b.txt", nil); r == nil || r.Kind != KindStatic {
		t.Fatal("b.txt not picked up")
	}
}

func TestAsset(t *testing.T) {
	_, p := newServer(t, "fixtures/core/pb_pages")
	cases := map[[2]string]string{
		{".", "/assets/app.css"}:          "/assets/app.97e2e94903cc.css",
		{".", "assets/app.css"}:           "/assets/app.97e2e94903cc.css",
		{"assets", "app.css"}:             "/assets/app.97e2e94903cc.css",
		{"(marketing)", "assets/app.css"}: "/assets/app.97e2e94903cc.css",
		{".", "/nope.css"}:                "/nope.css",
		{".", "/about"}:                   "/about",
	}
	for in, want := range cases {
		if got := p.Asset(in[0], in[1]); got != want {
			t.Errorf("Asset(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
