package pages

import (
	"strings"
	"testing"
)

func TestJSXPages(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+layout.jsx": `export default function Layout({ children, slot }) {
			return <html><head><title>{slot('title') || 'Site'}</title></head><body>{children}</body></html>
		}`,
		"_private/Card.tsx": `type P = { title: string; children?: any }
			export default function Card({ title, children }: P) { return <div class="card"><h2>{title}</h2>{children}</div> }`,
		"_private/util.ts": `export const shout = (s: string): string => s.toUpperCase() + '!'`,
		"index.tsx": `import Card from './_private/Card'
			import { shout } from './_private/util'
			interface Item { name: string }
			export default function Page({ data, slot, asset }: any) {
				slot('title', <>Home &amp; more</>)
				const items: Item[] = data.items
				return <main>
					<Card title={shout('hi')}>
						<ul>{items.map((i) => <li key={i.name} className="it">{i.name}</li>)}</ul>
					</Card>
					<p data-x={'"q"'} hidden={false} disabled style={{ marginTop: 4 }}>{'<b>esc</b>'}</p>
					<div dangerouslySetInnerHTML={{ __html: '<i>raw</i>' }} />
					<img src={asset('logo.png')} alt="" />
				</main>
			}`,
		"+page.ts":  `export function get(ctx: any) { ctx.data.items = [{ name: 'a<' }, { name: 'b' }] }`,
		"logo.png":  "png",
		"mixed.ejs": `<%- include('Card', { title: 'from ejs' }) %>`,
	})
	code, body := get(t, dir, "/", true)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, want := range []string{
		`<html><head><title>Home &amp; more</title></head><body><main>`,
		`<div class="card"><h2>HI!</h2><ul><li class="it">a&lt;</li><li class="it">b</li></ul></div>`,
		`<p data-x="&#34;q&#34;" disabled style="margin-top:4">&lt;b&gt;esc&lt;/b&gt;</p>`,
		`<div><i>raw</i></div>`,
		`<img src="/logo.`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in\n%s", want, body)
		}
	}
	code, body = get(t, dir, "/mixed", true)
	if code != 200 || !strings.Contains(body, `<body><div class="card"><h2>from ejs</h2></div></body>`) {
		t.Fatalf("ejs including tsx: %d %s", code, body)
	}
}

func TestTSMiddleware(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+config.ts":     "const config: { debug: boolean } = { debug: false }\nexport default config",
		"+middleware.ts": "export default (ctx: any, next: () => void) => { ctx.data.mw = 'ts'; return next() }",
		"index.ejs":      "<%= data.mw %>",
	})
	if code, body := get(t, dir, "/", true); code != 200 || body != "ts" {
		t.Fatalf("%d %s", code, body)
	}
}

func TestJSXSyntaxErrorNamesFile(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.tsx": "export default () => <div>"})
	code, body := get(t, dir, "/", true)
	if code != 500 || !strings.Contains(body, "index.tsx:1") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestOneLayoutPerFolder(t *testing.T) {
	dir := writeTree(t, map[string]string{"+layout.ejs": "a", "+layout.tsx": "export default () => null", "index.ejs": "x"})
	_, errs := Build(dir)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "only one +layout") {
		t.Fatalf("%v", errs)
	}
}
