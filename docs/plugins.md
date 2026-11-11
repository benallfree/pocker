# Create your own plugin

A PockerBase plugin is Go code compiled into your build. It can add, to every instance:
- Go routes;
- PocketBase hooks;
- collections;
- admin dashboard screens.

It can also run process-wide jobs and middleware.

> **Status.** Everything below works today through `pocker.Server`. A packaged plugin API (one `Plugin` interface,
> per-plugin config, a `pocker.Main(plugins...)` entry point) is planned. It will wrap these same hooks, so code
> written this way moves over with little change.

A complete, runnable example is in [`examples/pocker-extension`](../examples/pocker-extension).

## Compiled in, not loaded at run time

Plugins are Go packages linked into the binary, the way Caddy modules work. Go's `plugin` package (`.so` files)
isn't used:
- it needs identical toolchain and dependency versions;
- it can't unload;
- it rules out static `CGO_ENABLED=0` builds.

A build is a small `main.go` that starts a `pocker.Server` with your code.

## Your module

PockerBase keeps PocketBase's module path, so upstream merges stay clean. Point it at PockerBase in your `go.mod`:

```
require ganigeorgiev.com/pocketbase/pocketbase v0.41.1

replace ganigeorgiev.com/pocketbase/pocketbase => github.com/pockerbase/pocketbase <commit or version>
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

## Rules for plugins in a multitenant build

Your Go code runs with the host's full power, outside the JS sandbox:
- **Act only for the instance you were called for.** Use `inst.ID` and `Manager.InstanceDir(inst.ID)`; never build a
  path from request input.
- **Instance data needs instance auth.** Protect routes that read or change it with PocketBase's auth middlewares
  (`apis.RequireSuperuserAuth()`, record auth).
- **Exposing something to JS is a sandbox decision.** Expose a function to an instance's JS only if it's safe for a
  hostile tenant to call. See [multitenant.md](multitenant.md).
- **Outbound calls follow the egress policy.** Go's default HTTP client already refuses private addresses. If your
  plugin must reach a private service, give it its own `http.Client` and transport, and never let instance input pick
  the address.

## Planned plugins

Our own plugins will live in this repo under [`ext/`](../ext), each as its own Go module (`github.com/pockerbase/pocketbase/ext/<name>`) with its own version tags. The list and what each will do is in [ext/README.md](../ext/README.md).
