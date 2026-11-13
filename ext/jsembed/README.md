# jsembed

Run an app's JS hooks and migrations from an `fs.FS`, for example files embedded in the binary, instead of
`pb_hooks` and `pb_migrations` on disk.

**Runs on:** Single app. (PocketHost's mothership binary uses it for its own hooks.)

## Use

```go
//go:embed hooks
var files embed.FS

hooks, _ := fs.Sub(files, "hooks")
jsvm.MustRegister(app, jsembed.Config(hooks, migrations, jsvm.Config{OnInit: ...}))
```

Either FS may be nil to keep jsvm's own behavior for that half. [`example/`](example/main.go) serves a route from
embedded hooks: `go run ./example serve`, then `curl http://127.0.0.1:8090/hello`.

## What it does

- `.pb.js` files come only from the hooks FS's root: `HooksDir` is ignored, nothing is written to disk, no watching.
  Migrations come only from the migrations FS's root.
- `__hooks` is a virtual root, `/pb_embedded/pb_hooks`, that is never looked up on disk. `require()` under it is
  served from the hooks FS:
  - `require(__hooks + "/lib/x.js")` loads the embedded file, and a module's own `require("./y.js")` stays inside;
  - a relative `require` from a `.pb.js` handler is anchored at `__hooks`;
  - `..` escapes, absolute disk paths and `node_modules` lookups are "module not found";
  - a real folder at `/pb_embedded/...` makes no difference.
- JS that reads files itself (`$os.readFile(__hooks + ...)`) gets "no such file", on purpose.

## No change to PocketBase's behavior

It plugs into three optional `jsvm.Config` fields, `HookFiles`, `MigrationFiles` and `Require` (PocketBase CE's
small seam; unset, jsvm behaves like upstream). It used to live inside jsvm (`HooksFS`/`MigrationsFS`); it moved
here on 2026-11-13.
