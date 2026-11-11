# pages

Server-rendered multi-page apps from a `pb_pages` folder (PocketPages v1), with routing, static files and templates
in Go and page code in the app's JS VM.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **In progress.** Working today: the `pb_pages` route table (pages, `+api.js`, static files, groups, `[param]`s,
> layout and middleware chains, startup errors for bad names and conflicts), static files with ETags and
> fingerprinted URLs, and live rescans when `pb_pages` changes. Not yet: rendering (middleware, handlers, EJS,
> layouts); page and API routes answer `501` until it lands.

- The contract: [SPEC.md](SPEC.md).
- Test fixtures: [fixtures/](fixtures) (`go test ./...` runs every request case; ones that need rendering are
  skipped for now).
- Starter kits: [starters/](starters). The PocketPages site: [site/](site).

## Use it

```go
app := pocketbase.New()
pages.MustRegister(app, pages.Config{}) // serves pb_pages next to pb_data
app.Start()
```

Module `github.com/pockethost/pocketbase/ext/pages`. See [example/main.go](example/main.go) and
[Create your own plugin](../../docs/plugins.md).

## License

MIT
