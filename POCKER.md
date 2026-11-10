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
max_js_execution: 30s     # any single JS call (hook, route, cron, migration)
max_query_duration: 30s   # any SQL statement without its own deadline
egress_allow: []          # private networks apps may still reach (default none)
http_send_max_timeout: 60s
http_send_max_body: 10485760
```

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
| Outbound network: `$http.send`, `$filesystem.fileFromURL`, S3 storage and backups, OAuth2/JWK fetches, SMTP | Public addresses only, checked on the address actually connected to (after DNS, so rebinding doesn't help): loopback, private ranges, link-local and cloud metadata, CGNAT, multicast, reserved and documentation ranges, and IPv6 equivalents/IPv4-in-IPv6 are refused unless listed in `egress_allow`. Installed process-wide on Go's default HTTP transport and PocketBase's SMTP client (`pocker/egress.go`). `$http.send` timeouts and response bodies are capped. |

**Not covered yet:** per-instance memory (goja has no heap limit; a hook can still allocate the process into GC
pressure or OOM), per-instance concurrency limits, and OS-level defence in depth: run Pocker as a non-root user with an
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
