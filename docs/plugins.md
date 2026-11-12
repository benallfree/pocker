# Create your own plugin

A PocketBase CE plugin is Go code compiled into your build. It can add, to every instance:
- Go routes;
- PocketBase hooks;
- collections;
- admin dashboard screens.

It can also run process-wide jobs and middleware.

## The plugin contract: `ceplugin`

Write a plugin once, against [`ceplugin`](../ceplugin/ceplugin.go) in core, and the same value runs on a single app
and on every instance under pocker:

```go
type Plugin struct{ cfg Config }

func New(cfg Config) *Plugin { return &Plugin{cfg: cfg} }

func (p *Plugin) Name() string { return "hello" } // config key, `_hello_` collections, logs

func (p *Plugin) Register(app core.App, cfg ceplugin.Config) error {
    c := p.cfg
    if err := cfg.Decode(&c); err != nil { // this app's settings over the defaults (json tags)
        return err
    }
    app.OnServe().BindFunc(func(se *core.ServeEvent) error { /* routes, UI extensions */ return se.Next() })
    return nil
}

// optional: a $hello global in the app's JS hooks
func (p *Plugin) BindJS(app core.App, vm *goja.Runtime) { /* vm.Set("$hello", ...) */ }
```

- **Single app:** `ceplugin.MustRegister(app, plugins...)`, and `ceplugin.BindJS(app, vm, plugins...)` in jsvm's
  `OnInit`. The standard binary does this for the plugins it ships ([`cmd/pocketbase`](../cmd/pocketbase/main.go)).
- **Pocker:** put the plugin in `Server.Plugins`. Pocker calls `Register` for every instance with that instance's
  settings: the config file's top-level `<name>:` key, then the instance's `plugins.<name>`, over the defaults given
  to your constructor. `enabled: false` (the instance wins) means `Register` and `BindJS` are never called for it.
- **No pocker import:** a contract plugin doesn't depend on ext/pocker, so single-app builds don't link it. Reach for
  the host interfaces below only for what's process-wide (a request gate, TLS, background jobs).
- [bulkmail](../ext/bulkmail/plugin.go) is the reference implementation.

> **Status.** `ceplugin` covers per-app plugins. The process-wide host interfaces below (`pocker.Server` fields and
> `Server.Plugins` host interfaces) are pocker's for now; a `pocketbase.Main(plugins...)` entry point is planned.

A complete, runnable example is in [`ext/pocker/example`](../ext/pocker/example).

## Compiled in, not loaded at run time

Plugins are Go packages linked into the binary, the way Caddy modules work. Go's `plugin` package (`.so` files)
isn't used:
- it needs identical toolchain and dependency versions;
- it can't unload;
- it rules out static `CGO_ENABLED=0` builds.

A build is a small `main.go` that starts a `pocker.Server` with your code.

## Your module

PocketBase CE keeps PocketBase's module path, so upstream merges stay clean. Point it at PocketBase CE in your `go.mod`:

```
require ganigeorgiev.com/pocketbase/pocketbase v0.41.1

replace ganigeorgiev.com/pocketbase/pocketbase => github.com/pockethost/pocketbase <commit or version>
```

Go only honors `replace` in the main module, so the final binary's `go.mod` needs this line, not only your
plugin's.

## Per instance: `Server.SetupApp`

`SetupApp` runs for every instance's app after its migrations and before it serves. Bind hooks and routes there, as
you would in a stand-alone PocketBase `main.go`:

```go
srv := &pocker.Server{
    Config: cfg,
    SetupApp: func(inst pocker.Instance, app core.App) error {
        app.OnServe().BindFunc(func(se *core.ServeEvent) error {
            se.Router.GET("/api/hello", func(e *core.RequestEvent) error {
                return e.JSON(http.StatusOK, map[string]string{"instance": inst.ID})
            }).Bind(apis.RequireSuperuserAuth())
            return se.Next()
        })

        app.OnRecordCreate("posts").BindFunc(func(e *core.RecordEvent) error {
            // ...
            return e.Next()
        })
        return nil
    },
}
```

- `inst` is the instance's config from the instances file or your provider: id, hosts and `inst.Private`.
- Private values are for your Go code only and are never visible to the instance's JS. Use them for per-instance
  tokens your plugin needs.
- If your plugin needs its own collections, create them in a migration or on bootstrap. Prefix them with `_<plugin>_`
  so they never collide with the app's collections, and make them superuser-only unless they must be public.

## Admin screens: `UIExtensions`

The PocketBase dashboard loads extensions before it starts its router:
- Each extension is a named file system served at `/_/extensions/<name>/`.
- Every extension's `main.js` is bundled into `/_/extensions.js`.

Add yours in the same `OnServe` hook:

```go
//go:embed ui
var uiFiles embed.FS

ui, _ := fs.Sub(uiFiles, "ui")
se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: "hello", FS: ui})
```

`ui/main.js` runs in the dashboard with its UI kit (`t.*`) and globals (`app.routes`, `app.store`, `app.pb`):

```js
// a new page, with a link in the header
app.routes.superuserOnly("#/hello", async () => {
    const res = await app.pb.send("/api/hello");
    return t.div({ className: "page" },
        t.div({ className: "page-content" }, t.h1({}, "Hello"), t.p({}, `Instance ${res.instance}`)),
    );
});
app.store.headerLinks.push({ href: "#/hello", icon: "ri-emotion-happy-line", label: "Hello" });

// add to an existing screen: screens marked with pbEvent fire mount:<name> with their element
document.addEventListener("mount:pageBackupsSettings", (e) => {
    e.detail.prepend(t.p({ className: "txt-hint" }, "Hello from an extension."));
});
```

- **More hooks:** `app.store.settingsNavGroups` adds entries to the Settings sidebar. Search `pbEvent:` in
  [`ui/src`](../ui/src) for every screen you can add to (Backups exposes `pageBackupsSettings`, `backupsList`,
  `backupsSettingsForm`, `backupCreateModal` and `backupRestoreModal`).
- **No build step:** plain JS works. Bundle with Vite if you prefer.
- **Experimental upstream:** this UI extension API is undocumented and can change between PocketBase releases
  (`ui/README.md`). Keep `main.js` small, and test it after each upstream update.

## Process-wide: middleware, jobs, events

| Field | Use it for |
|---|---|
| `Server.Plugins` | Packaged host plugins (`Configurer`, `Gate`, `TLSProvider`, `Wrapper`, `Starter`, `AppIniter`; see [POCKER.md](../ext/pocker/POCKER.md#plugins)), e.g. [autotls](../ext/autotls), [servertiming](../ext/servertiming), [pitr](../ext/pitr). |
| `Server.Wrap` | Middleware around every instance request, e.g. auth in front of all instances, metrics, headers. |
| `Server.OnStart` | Gets the `Manager` before the first request: start background jobs (schedules, sweeps) in your own goroutines. |
| `Server.OnEvent` | Instance events: started, stopped, memory strikes, pauses. |
| `Server.Source` | Your own `InstanceSource`: instances from a database or control plane instead of `instances.yaml`. |
| `Server.Forward` | How requests for instances running on another server are forwarded. |

From the `Manager`, host code can:
- change an instance's files safely: `WithInstanceStopped(ctx, id, fn)` stops it, holds its requests, runs `fn`, then
  starts a fresh app;
- start it: `StartInstance`;
- find its folder: `InstanceDir`;
- write to its log: `LogInstance`.

## Rules for plugins that host untrusted instances

Your Go code runs with the host's full power, outside the JS sandbox:
- **Act only for the instance you were called for.** Use `inst.ID` and `Manager.InstanceDir(inst.ID)`; never build a
  path from request input.
- **Instance data needs instance auth.** Protect routes that read or change it with PocketBase's auth middlewares
  (`apis.RequireSuperuserAuth()`, record auth).
- **Exposing something to JS is a sandbox decision.** Expose a function to an instance's JS only if it's safe for a
  hostile tenant to call. See [How is this safe?](../ext/sandbox/README.md#how-is-this-safe).
- **Outbound calls follow the egress policy.** Go's default HTTP client already refuses private addresses. If your
  plugin must reach a private service, give it its own `http.Client` and transport, and never let instance input pick
  the address.

## Contributing a plugin

Plugins are welcome, from us and from you. Open a pull request that adds `ext/<your-plugin>/`.

### Change PocketBase's core as little as possible

PocketBase CE stays close to upstream so every PocketBase release merges cleanly. If your plugin needs something the
core doesn't offer yet:
- **Add the smallest hook that makes it possible:** an extension point or an override, not your plugin's logic. A
  nil-checked function field, a list callers can append to, or an event, with upstream behavior unchanged when nothing
  is registered.
- **Make it generic.** Name it for what it allows ("storage backend for an app"), not for your plugin. Assume someone
  else's plugin will use it too, and that we'll offer it to upstream PocketBase.
- **Mark every changed line** in PocketBase's own files with `// CE:` and a few words on why, so the next upstream
  merge knows what to keep.
- **Keep it separate:** the core change goes in its own commit, with a test, ahead of the plugin.
- **Prefer what exists:** jsvm `OnInit`, `OnServe`, the record and request hooks, `BaseAppConfig.DBConnect`,
  `UIExtensions`. Many plugins need no core change at all.

### What a plugin folder contains

```
ext/<name>/
  go.mod            module github.com/pockethost/pocketbase/ext/<name>
  README.md         what it does, runs on (single app, pocker or both), config
  <name>.go         the plugin
  <name>_test.go
  example/main.go   a minimal program that builds cleanly and shows the feature
  ui/               optional: admin screens (embedded main.js)
```

- **`example/main.go` is required.** Keep it as small as possible: just enough to build a binary that shows the
  feature working, with no extra harness. `go build ./example` and `go vet ./...` must pass with no warnings.
  [`ext/pocker/example`](../ext/pocker/example) and [`ext/sandbox/example`](../ext/sandbox/example) are the size to aim for.
- **Examples come with two logins:** superuser `admin@example.com` and user `user@example.com` (verified, in
  `users`), both with password `password`. Every `example/main.go` calls
  `devusers.Register(app)` right after `pocketbase.New()` (under pocker, `devusers.Ensure(app)` in
  `SetupApp`), from [`ext/devusers`](../ext/devusers/devusers.go). A fresh data dir is then ready to log
  in to, with no installer page and no CLI step. Never register it in a production build.
- **Dependencies stay in your module.** Heavy libraries go in your plugin's `go.mod`, never in core's.
- **Single app first.** Where it makes sense, a plugin should work on one PocketBase app, and add more under the
  pocker (multitenant) host.
- **MIT**, like PocketBase and the rest of this repo.

## Planned plugins

Our own plugins will live in this repo under [`ext/`](../ext), each as its own Go module (`github.com/pockethost/pocketbase/ext/<name>`) with its own version tags. The list and what each will do is in [ext/README.md](../ext/README.md).
