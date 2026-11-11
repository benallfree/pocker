package pages

import (
	"strings"
	"testing"
)

func TestRequireNodeModules(t *testing.T) {
	dir := writeTree(t, map[string]string{
		// a plain package with "main"
		"node_modules/slug/package.json": `{"name":"slug","main":"lib/slug.js"}`,
		"node_modules/slug/lib/slug.js":  `module.exports = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, '-')`,
		// index.js fallback, and a subpath
		"node_modules/plain/index.js": `module.exports = 'plain'`,
		"node_modules/plain/extra.js": `module.exports = 'extra'`,
		// scoped, with conditional exports and a subpath export
		"node_modules/@acme/fmt/package.json": `{"exports":{".":{"import":"./esm.mjs","require":"./cjs.js"},"./money":"./money.js"}}`,
		"node_modules/@acme/fmt/cjs.js":       `module.exports = { hello: (n) => 'hi ' + n }`,
		"node_modules/@acme/fmt/money.js":     `module.exports = (c) => '$' + (c / 100).toFixed(2)`,
		"node_modules/@acme/fmt/secret.js":    `module.exports = 'not exported'`,
		// a package that tries to point outside itself
		"node_modules/evil/package.json": `{"main":"../../_private/x.js"}`,
		"_private/x.js":                  `module.exports = 'x'`,
		// a nearer node_modules wins
		"blog/node_modules/plain/index.js": `module.exports = 'blog plain'`,

		"index.ejs":       `<%= require('slug')('Hello World!') %>|<%= require('plain') %>|<%= require('plain/extra') %>|<%= require('@acme/fmt').hello('Ben') %>|<%= require('@acme/fmt/money')(1999) %>`,
		"blog/index.ejs":  `<%= require('plain') %>`,
		"mod.ejs":         `<%= require('./_private/use.js') %>`,
		"_private/use.js": `module.exports = require('slug')('From A Module')`,
		"tsx.tsx": `import slug from 'slug'
			export default () => <p>{slug('From TSX')}</p>`,
		"hidden.ejs": `<%= require('@acme/fmt/secret') %>`,
		"evil.ejs":   `<%= require('evil') %>`,
		"climb.ejs":  `<%= require('slug/../../_private/x') %>`,
		"rel.ejs":    `<%= require('./_private/x') %>`,
	})
	cases := map[string]string{
		"/":     "hello-world-|plain|extra|hi Ben|$19.99",
		"/blog": "blog plain",
		"/mod":  "from-a-module",
		"/tsx":  "<p>from-tsx</p>",
		"/rel":  "x",
	}
	for p, want := range cases {
		code, body := get(t, dir, p, true)
		if code != 200 || body != want {
			t.Fatalf("%s: %d %q, want %q", p, code, body, want)
		}
	}
	// not exported, pointing outside the package, or climbing: never resolved from pb_pages; the app's own loader
	// then fails to find them too
	for _, p := range []string{"/hidden", "/evil", "/climb"} {
		code, body := get(t, dir, p, true)
		if code != 500 || strings.Contains(body, "not exported") || strings.Contains(body, ">x<") {
			t.Fatalf("%s: %d %.200q", p, code, body)
		}
	}
}
