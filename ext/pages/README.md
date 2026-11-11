# pages

Server-rendered multi-page apps from a `pb_pages` folder (PocketPages v1), with routing, static files and templates
in Go and page code in the app's JS VM.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **In progress.** Working: the `pb_pages` route table, static files with ETags and fingerprints, live rescans,
> rendering (middleware, `+page.js` / `+api.js`, EJS compiled in Go, JSX/TSX and TypeScript via built-in esbuild, includes, slots, layouts, Markdown, `ctx`,
> `+error.ejs`, `+404.ejs`, Markdown images through `asset()`), JS plugins, `ctx.res.sse()`, the built-in `realtime` and `auth` plugins (password, OTP, anonymous,
> register, OAuth2, email flows, cookie sessions), `ctx.pb()` (the bundled JS SDK, in-process, as the visitor) and the
> Datastar add-on. Every starter kit and the site render. Not yet:
> MFA in `ctx.session`, JS plugins' `handles()`, `pages create`, and runs under pocker + the sandbox.

- The contract: [SPEC.md](SPEC.md).
- Test fixtures: [fixtures/](fixtures) (`go test ./...` runs every request case).
- Starter kits: [starters/](starters). Add-ons: [addons/](addons). The PocketPages site: [site/](site).

## Speed

`ext/pages` against npm PocketPages 0.22.5 (the last 0.x release), serving the same starter kit pages: the 0.x kits
in `pb_hooks/pages` on stock PocketBase 0.41.1, the v1 ports in `pb_pages` on the CE binary.

| Page | Version | Requests/s (50 concurrent) | p50 | p99 | One at a time (median) |
|---|---|---:|---:|---:|---:|
| `minimal` `/` (EJS) | npm 0.22.5 | 620 | 30 ms | 670 ms | 5.7 ms |
| | **ext/pages** | **10,850** | **1.8 ms** | **37 ms** | **0.2 ms** |
| `daisyui-docs` `/docs/overview` (layouts, Markdown, partial) | npm 0.22.5 | 580 | 28 ms | 1.1 s | 6.5 ms |
| | **ext/pages** | **6,510** | **4.1 ms** | **49 ms** | **0.4 ms** |
| PocketBase's own `/api/health` (the ceiling) | CE | 55,000 | 0.9 ms | 2.4 ms | 0.1 ms |

About **17.5× more requests per second** on the simple page and **11×** on the docs page, with p99 latency 20× lower.
Single requests take 0.2 to 0.4 ms instead of 6 ms. Memory after each run was similar (170 to 350 MB for both).

Why: 0.x parsed routes, ran EJS through a JS implementation and loaded its plugins inside the JS VM on every request.
v1 builds the route table once, compiles each template once (EJS to JS in Go, Markdown with goldmark) and enters the
JS VM once per request.

How it was measured (2026-11-11): Apple M1 Max, 10 cores, macOS 27, Go 1.27.4, `hey` against `127.0.0.1`. Each server
was warmed with 500 requests, then `hey -z 10s -c 50` and `hey -n 2000 -c 1`; two rounds, which agreed within 5%.
Every response was a 200 with the full page. The 0.x docs kit inlines about 6 KB of CSS into each page where v1
links `app.css`. Rerun it with [`bench/run.sh`](bench/run.sh):

```bash
cp -R starters/daisyui-docs /tmp/v1-docs
pocketbase superuser upsert admin@example.com password --dir=/tmp/v1-docs/pb_data  # the CE binary
bench/run.sh v1-docs 8804 /docs/overview "$(which pocketbase)" /tmp/v1-docs
```

The demo in [`example/`](example) has a live speed check at `/speed`, which loads its own pages from your browser.

## Use it

The standard CE binary ([`cmd/pocketbase`](../../cmd/pocketbase/main.go)) includes pages: put a `pb_pages` folder next
to `pb_data` and run `pocketbase serve`. `--pagesDir` points it somewhere else. In your own Go build:

```go
app := pocketbase.New()
jsvm.MustRegister(app, jsvm.Config{})    // page code runs in the app's JS VM
pages.MustRegister(app, pages.Config{}) // serves pb_pages next to pb_data
app.Start()
```

Module `github.com/pockethost/pocketbase/ext/pages`. See [example/main.go](example/main.go) and
[Create your own plugin](../../docs/plugins.md).

## License

MIT
