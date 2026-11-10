# Pocker

Pocker hosts many PocketBase instances in one process. It is PocketBase itself (this repo tracks
[upstream PocketBase](https://ganigeorgiev.com/git/pocketbase/pocketbase) and shares its history) plus a small engine in
[`pocker/`](pocker) and a CLI in [`cmd/pocker/`](cmd/pocker).

- One port, many instances. Each request's hostname maps to a canonical instance id.
- Each instance is a normal PocketBase folder, `<pbRoot>/<id>` (`pb_data`, `pb_hooks`, `pb_migrations`, `pb_public`),
  with its own environment for JS hooks (`$os.getenv`, `process.env`), its own JS migrations and its own settings
  encryption key.
- An instance's app starts on its first request (tens of milliseconds) and keeps running.

## Dev mode: any hostname

```sh
go build -o pocker ./cmd/pocker
./pocker serve --dev --pbRoot ./pb_root
curl -H 'Host: blog.localhost' http://127.0.0.1:8090/api/health   # creates and serves ./pb_root/blog.localhost
```

A hostname the instance source doesn't map uses the hostname itself as its id (lowercase, no port, no trailing dot;
anything that isn't a safe folder name gets 400), its folder is created on first use, and apps run in PocketBase dev mode. The instances file
(or another provider) is optional in dev mode; hosts it maps keep their own ids.

## Production: mapped hostnames only

Without `--dev`, Pocker serves only what an instance source maps. The built-in source is a YAML file (reloaded when it
changes; Pocker refuses to start without it):

```yaml
# instances.yaml
instances:
  - id: blog                      # folder <pbRoot>/blog
    hosts: [blog.example.com, www.blog.example.com]
  - id: shop
    hosts: [shop.example.com]
    env: { STRIPE_KEY: sk_live_..., PB_ENCRYPTION_KEY: 0123456789abcdef0123456789abcdef }
  - id: old
    hosts: [old.example.com]
    blocked: { status: 402, message: "Payment required" }
```

```sh
./pocker serve --pbRoot ./pb_root --instances ./instances.yaml
```

Unknown hosts get 404 and no folder is ever created from a request. Ids are lowercase letters, digits, dots and dashes.
Hosting platforms plug in their own [`InstanceSource`](pocker/instance.go) (for example a control plane streaming the
hostname -> id map over SSE) instead of the file.

Optional `pocker.yaml` (every field has a default):

```yaml
listen: ":8090"
pb_root: "./pb_root"
dev: false
instances_file: "./instances.yaml"
hooks_pool_size: 2
shutdown_timeout: 10s
egress_allow: []          # private networks apps may still reach (default none)
memory_limit: 0           # Go soft memory limit (GOMEMLIMIT) in bytes; 0 = leave as is
memory_soft_limit: 0      # live heap where the memory watch steps in; 0 = 70% of memory_limit or 1 GiB

# default limits for every instance (each instance can override them, see below)
max_js_execution: 30s     # any single JS call (hook, route, cron, migration)
max_js_allocation: 64MiB  # one-shot JS allocations
max_call_stack_size: 2000
max_sleep: 10s
max_query_duration: 30s   # any SQL statement without its own deadline
http_send_max_timeout: 60s
http_send_max_body: 10MiB
quarantine: { strikes: 3, window: 10m, first_pause: 30s, max_pause: 15m, reset_after: 1h }   # or: quarantine: off
```

Each instance can override any of those limits (zero or missing fields keep the default):

```yaml
instances:
  - id: shop
    hosts: [shop.example.com]
    limits:
      max_js_execution: 60s
      max_js_allocation: 128MiB
      quarantine: { strikes: 5 }
  - id: my-own-app
    hosts: [mine.example.com]
    limits: { quarantine: off }   # only interrupt, never pause
```

## Platform values and logs

- **Superusers** (`superusers: [{email, password_hash, id?, token_key?}]`) are upserted into the app's `_superusers`
  in Go when it starts, e.g. so a hosting platform's login works in the dashboard. Nothing goes through env.
- **Private values** (`private: {...}`) are for the host application's Go code and never visible to JS. A host
  registers that code with `Server.SetupApp(inst, app)`, which runs after migrations and before the app serves (for
  example a backup route checked against `inst.Private["backup_token"]`).
- **Env** (`env: {...}`) is what the instance's JS sees through `$os.getenv` / `process.env`, and nothing else.
- **Logs:** PocketBase's own logs (the dashboard's Logs) are per instance in its `pb_data`. `console.*` from JS hooks
  goes to `<instance>/logs/exec.log` as JSON lines `{message, stream, time}` (`stdout` for log/info/debug, `stderr`
  for warn/error), rotated to `exec.log.1` at 10 MiB. A start failure (e.g. a broken hooks file) is written there too,
  and only that instance fails.

When an instance's `env`, `limits`, `dev`, `dir`, `superusers` or `private` changes, its app restarts on the next request (the old one finishes
in-flight requests first), so a rotated secret takes effect immediately. Host changes apply without a restart.

`GET /_pocker/health` on any host reports `{status, instances, running}`.

## Sandbox

Every instance's JS hooks and migrations run in a sandbox (`plugins/jsvm/sandbox.go`), and its SQL runs behind
SQLite's authorizer (`pocker/sqlguard.go`). [`pocker/sandbox_test.go`](pocker/sandbox_test.go) runs one attack per
route from one instance against another instance and the host; all of these are refused:

| Area | What the sandbox does |
|---|---|
| Files: `$os.*`, `$filepath.glob/walk*`, `$filesystem.fileFromPath`, `$template.loadFiles`, `$apis.static`, `require()` | Confined to the instance folder with an `os.Root`: the kernel resolves every path beneath it, refusing `..` and symlinks that leave it. Relative paths and absolute paths inside the folder (`__hooks + "/x"`) work as before. |
| Host primitives: `$os.cmd`, `$os.exec`, `$os.exit`, `$os.args`, `$filesystem.local` | Disabled (`$os.args` is empty). |
| Environment: `$os.getenv`, `process.env` | Only the instance's own `env`. |
| Go internals: `$app.restart/bootstrap/runAllMigrations/onServe/onTerminate/onBootstrap/cron/unsafeWithoutHooks`, `hook.bindFunc/bind/trigger/unbind*`, `*sql.DB/Conn/Tx/Stmt` (e.g. `dbx.DB.DB()`), `database/sql/driver`, `os/exec`, `net`, `syscall`, `http.Server` controls | Hidden from JS (the field-name mapper maps them to nothing). Top-level `onRecord*`, `routerAdd`, `cronAdd` and the rest of `$app` work as usual. |
| Raw SQL (`$app.db().newQuery`) | Allowed. SQLite's authorizer refuses `ATTACH` (and `VACUUM INTO`) outside the instance's `pb_data`, `PRAGMA temp_store_directory/data_store_directory` and `load_extension`; the CLI-only `readfile`/`writefile`/`fsdir` don't exist in the library. Statements without a deadline get `max_query_duration`. |
| CPU | Every JS call is interrupted after `max_js_execution`; `sleep()` is capped at 10 s. |
| Memory, one shot | Built-ins that allocate a large result in one step refuse results over `max_js_allocation`: `String.repeat/padStart/padEnd`, `Array.join/toString`, `ArrayBuffer`, typed arrays (incl. array-likes, `.from`, and reaching the constructor through an instance), `Buffer.alloc*` / `new Buffer`, `$os.readFile`, `fileFromPath`, `readerToString`/`toBytes` limits. Recursion depth is capped (a JS stack overflow would otherwise crash the Go process). |
| Memory, gradual | goja has no per-VM heap accounting, so `pocker/memwatch.go` watches the process heap every 50 ms. Above `memory_soft_limit` and still growing, it interrupts JS calls running longer than 100 ms and strikes their instances (above 1.4x, every JS call). With the instance's quarantine policy (default 3 strikes in 10 minutes) it is paused: its app is stopped and requests get 503 with `Retry-After`. Pauses back off (30 s, 1 m, 2 m, ... up to 15 m) and reset after an hour without strikes; `quarantine: off` only interrupts. Strikes, pauses and restarts are reported as events (`Server.OnEvent`, or an instance source implementing `EventSink`). |
| Outbound network: `$http.send`, `$filesystem.fileFromURL`, S3 storage and backups, OAuth2/JWK fetches, SMTP | Public addresses only, checked on the address actually connected to (after DNS, so rebinding doesn't help): loopback, private ranges, link-local and cloud metadata, CGNAT, multicast, reserved and documentation ranges, and IPv6 equivalents/IPv4-in-IPv6 are refused unless listed in `egress_allow`. Installed process-wide on Go's default HTTP transport and PocketBase's SMTP client (`pocker/egress.go`). `$http.send` timeouts and response bodies are capped. |

**Limits of the memory controls:** the watch attributes growth to whoever is running JS at the time (usually right,
not guaranteed), and a hook that allocates faster than the 50 ms sample through a path not covered by the one-shot caps
could still push the process over its limit. Set `memory_limit` and run Pocker under a cgroup memory limit so the worst
case is a restart, not a host OOM.

**Not covered yet:** per-instance concurrency limits, and OS-level defence in depth: run Pocker as a non-root user with an
OS firewall that only lets that user reach public addresses (a second, independent egress layer), plus seccomp and
Landlock.

## Changes to PocketBase

Kept small and marked `// POCKER:` so upstream merges stay easy:

- `core.BaseAppConfig.AppMigrations`: a per-app migrations list (JS migrations from `plugins/jsvm` register there
  instead of the global `core.AppMigrations`).
- `core.BaseAppConfig.EncryptionKey`: the settings encryption key passed in directly instead of read from the process
  environment.
- `plugins/jsvm`: `Config.Sandbox` (all sandbox code is in `sandbox.go`); `pool.go` and `jsvm.go` call it at a few
  marked points (bindings, `require` loader, per-call time limit); `binds.go` has `HTTPSendMaxTimeout` /
  `HTTPSendMaxBodySize` caps for `$http.send`.
- `tools/mailer`: `DialContext`, when set, sends SMTP through the host's dialer (same exchange as mailyak).

## Upstream tracking

```sh
git remote add upstream https://ganigeorgiev.com/git/pocketbase/pocketbase
git fetch upstream --tags
git merge v0.41.2   # resolve conflicts in the POCKER-marked spots, rerun `go test ./pocker/... ./core/... ./plugins/jsvm/...`
```
