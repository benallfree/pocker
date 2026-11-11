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
