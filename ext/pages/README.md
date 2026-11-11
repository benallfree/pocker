# pages

Server-rendered multi-page apps from a `pb_pages` folder (PocketPages v1), with routing, static files and templates
in Go and page code in the app's JS VM.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **In progress.** Working: the `pb_pages` route table, static files with ETags and fingerprints, live rescans,
> and rendering: middleware, `+page.js` / `+api.js` handlers, EJS compiled in Go (cached per content hash), includes,
> slots and layouts, Markdown pages, `ctx` (request, response, cookies, flash, data, meta, assets, resolve, store,
> errors and `+error.ejs`), JS plugins. Not yet: the built-in `auth`, `realtime` and `datastar` plugins,
> `ctx.res.sse()`, `+404.ejs`, JS plugins' `handles()`, Markdown image paths through `asset()`, `pages create`, and
> runs under pocker + the sandbox.

- The contract: [SPEC.md](SPEC.md).
- Test fixtures: [fixtures/](fixtures) (`go test ./...` runs every request case).
- Starter kits: [starters/](starters). The PocketPages site: [site/](site).

## Use it

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
