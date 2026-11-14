# Pocker

Pocker hosts many PocketBase instances in one process. It is PocketBase itself (this repo tracks
[upstream PocketBase](https://ganigeorgiev.com/git/pocketbase/pocketbase) and shares its history) plus a small engine in
this folder and a CLI in [`cmd/pocker/`](cmd/pocker).

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
    secrets: { STRIPE_KEY: sk_live_... }               # the app's environment
    encryption_key: 0123456789abcdef0123456789abcdef   # encrypts PocketBase's stored settings; not visible to JS
    access: { cors_origins: [https://shop.example.com] }
    plugins: { firewall: { trusted_ips: [203.0.113.0/24] } }   # settings for plugins, by name
  - id: old
    hosts: [old.example.com]
    blocked: { status: 402, message: "Payment required" }
```

```sh
./pocker serve --pbRoot ./pb_root --instances ./instances.yaml
```

Unknown hosts get 404 and no folder is ever created from a request. Ids are lowercase letters, digits, dots and dashes.
Hosting platforms plug in their own [`InstanceSource`](instance.go) (for example a control plane streaming the
hostname -> id map over SSE) instead of the file.

Optional `pocker.yaml` (every field has a default):

```yaml
listen: ":8090"
plain_listen: []          # extra plain-HTTP listeners (no TLS, no redirect), e.g. a private network other servers forward on
pb_root: "./pb_root"
dev: false
instances_file: "./instances.yaml"
hooks_pool_size: 2
shutdown_timeout: 10s
egress_allow: []          # private networks apps may still reach (default none)

# default limits for every instance (each instance can override them, see below)
max_js_execution: 30s     # any single JS call (hook, route, cron, migration)
max_js_allocation: 64MiB  # one-shot JS allocations
max_call_stack_size: 2000
max_sleep: 10s
max_query_duration: 30s   # any SQL statement without its own deadline
http_send_max_timeout: 60s
http_send_max_body: 10MiB
max_concurrent_js: 16     # JS calls an instance runs at once; more wait up to max_js_execution, then fail
# the keys below are read by plugins: rate_limits/firewall by ext/firewall, edges by ext/edge, tls.auto... by ext/autotls
rate_limits:              # per client IP and per instance; trusted clients get the trusted set
  ip_hourly: 1000
  instance_hourly: 10000
  ip_concurrent: 15
  instance_concurrent: 250
  file_weight: 0.1        # a /api/files request costs a tenth
  trusted: { ip_hourly: 5000, instance_hourly: 20000, ip_concurrent: 50, instance_concurrent: 250 }
  # off: true             # no rate limiting (the default in --dev unless rate_limits is set)

firewall:
  allow_cidrs: []               # peers allowed to connect (empty = everyone); others get 403
  trusted_proxies: []           # peers whose client-IP headers are believed
  client_ip_headers: [CF-Connecting-IP, True-Client-IP, X-Forwarded-For, X-Real-IP]
  trusted_clients: []           # client IPs with the trusted limits on every instance
  rate_limit_header_prefix: X-RateLimit

tls:
  # either certificate files (re-read when they change; no restart for renewals) ...
  cert: /path/fullchain.pem
  key: /path/privkey.pem
  # ... or automatic certificates (ACME, via CertMagic, Caddy's certificate engine):
  # auto: true
  # email: ops@example.com
  # ca: letsencrypt             # letsencrypt (default), letsencrypt-staging, zerossl, or an ACME directory URL
  # storage: ./pb_root/.pocker-certs
  # domains: ["*.example.com", "example.com"]   # obtained up front; wildcards need dns
  # dns: { provider: cloudflare, api_token_env: CF_API_TOKEN }   # only needed for wildcards
  http_listen: ":80"            # optional: plain HTTP redirects to HTTPS (and answers ACME HTTP-01 challenges)
```

**Firewall** ([ext/firewall](../firewall/README.md); `Server.Firewall`, on in the `pocker` command and whenever the config has `firewall:` or `rate_limits:`). Peers outside `allow_cidrs` get 403. The client IP comes from `client_ip_headers` only when the peer is
a trusted proxy, otherwise it is the peer. Rate-limited requests get 429 with `Retry-After` and a message naming the
limit; every routed response carries `<prefix>-{Ip,Instance}-{Hourly,Concurrent}-{Limit,Remaining}` and
`<prefix>-{Ip,Instance}-Hourly-Reset`, listed in `Access-Control-Expose-Headers`. Long-lived responses (realtime) hold
their concurrency slot until they end.

**TLS.** Certificate files are core. Automatic certificates are [ext/autotls](../autotls/README.md) (included in the
`pocker` command): with `auto: true` and no `dns`, every hostname the instance source maps gets its own certificate on its first
TLS handshake (HTTP-01 / TLS-ALPN-01), with any DNS host and no API token; hostnames no instance serves never trigger
issuance. A DNS provider (Cloudflare for now) is only needed for wildcard `domains`, proved through DNS-01; a wildcard
without one is a config error.

Each instance can override any of those limits (zero or missing fields keep the default):

```yaml
instances:
  - id: shop
    hosts: [shop.example.com]
    limits:
      max_js_execution: 60s
      max_js_allocation: 128MiB
  - id: api
    hosts: [api.example.com]
    access: { cors_origins: [https://app.example.com] }
    limits: { max_concurrent_js: 32 }
    plugins:
      firewall: { rate_limits: { ip_hourly: 5000, instance_concurrent: 500 }, trusted_ips: [203.0.113.0/24] }
```

## Plugins

`pocker.Server.Plugins` takes plugins (see `plugin.go`). Each implements `Name()` and any of:

| Interface | Used for |
|---|---|
| `Configurer` | reading its own keys from pocker.yaml (`Configure(doc, cfg)`) |
| `Gate` | a peer check before routing, and admission (e.g. rate limits) before an instance is served or forwarded |
| `TLSProvider` | the main listener's TLS (e.g. automatic certificates) |
| `Wrapper` | wrapping the server's handler (the first listed is outermost) |
| `Starter` | background work once the Manager exists |
| `AppIniter` | setting up each instance's app before it bootstraps (`InitApp(inst, app)`), e.g. [pitr](../pitr/README.md) binding `OnBootstrap`/`OnServe`/`OnTerminate`; runs on every start, including restarts |

Per-instance settings for a plugin go under the instance's `plugins.<name>` (read them with `pocker.DecodePluginConfig`).
`app.Restart()` (PocketBase's backup restore, pitr) restarts only that instance, not the process.
Timings for the Server-Timing header are registered with `pocker.Timing(ctx, fn)`. The `pocker` command
([`cmd/pocker`](cmd/pocker)) bundles [servertiming](../servertiming/README.md), the [firewall](../firewall/README.md) and
[autotls](../autotls/README.md).

## Platform values and logs

- **Superusers** (`superusers: [{email, password_hash, id?, token_key?}]`) are upserted into the app's `_superusers`
  in Go when it starts, e.g. so a hosting platform's login works in the dashboard. Nothing goes through env.
- **Private values** (`private: {...}`) are for the host application's Go code and never visible to JS. A host
  registers that code with `Server.SetupApp(inst, app)`, which runs after migrations and before the app serves (for
  example a backup route checked against `inst.Private["backup_token"]`).
- **Secrets** (`secrets: {...}`) are the instance's environment: what its JS sees through `$os.getenv` /
  `process.env`, and nothing else. No names are reserved; platform settings are explicit fields.
- **Encryption key** (`encryption_key`) encrypts PocketBase's stored settings (like `--encryptionEnv`). JS never sees it.
- **Access** (`access: {cors_origins}`) is the owner's say over which browser origins may call the instance
  (PocketBase matching: exact, `*`, wildcards; empty = any). Client IPs that get the trusted rate limits are the
  firewall's `plugins.firewall.trusted_ips` (on top of the process-wide `firewall.trusted_clients`).
- **Plugins** (`plugins: {<name>: ...}`) are per-instance settings for plugins; changing them doesn't restart the app.
- **Logs:** PocketBase's own logs (the dashboard's Logs) are per instance in its `pb_data`. `console.*` from JS hooks
  goes to `<instance>/logs/exec.log` as JSON lines `{message, stream, time}` (`stdout` for log/info/debug, `stderr`
  for warn/error), rotated to `exec.log.1` at 10 MiB. A start failure (e.g. a broken hooks file) is written there too,
  and only that instance fails.

When an instance's `secrets`, `encryption_key`, `access`, `limits`, `dev`, `dir`, `superusers`, `private` or `remote` changes, its app restarts on the next request (the old one finishes
in-flight requests first), so a rotated secret takes effect immediately. Host changes apply without a restart.

`GET /_pocker/health` on any host reports `{status, instances, running}`.

**Changing an instance's files from host code** (deploys, restores): `Manager.WithInstanceStopped(ctx, id, fn)` stops
the app, holds new requests for that instance, runs `fn(dir)`, then lets them through to a fresh app.
`Manager.StartInstance(ctx, id)` starts it right away and returns the start error (for example to tell a deploy that
a hook is broken), `Manager.Running(id)` says whether it is started, and `Manager.InstanceDir(id)` gives its folder.
`Manager.Ready()` says whether the instance list has loaded. `Manager.LogInstance(id, stream, message)` appends to the instance's `logs/exec.log` (e.g. a scheduled job's result).
Host code that runs in its own goroutines gets the Manager from `Server.OnStart`, before any request is served.

**Several servers, one set of instances.** An instance with `remote: <url>` runs on another server: Pocker checks the
peer and the instance's rate limits here, then forwards the request (a reverse proxy to the URL by default;
`Server.Forward` replaces it, e.g. to hold requests while the other server restarts). Its app never starts here, and
an app already running here stops when `remote` is set. `plain_listen` gives the other servers a plain-HTTP port on a
private network, and `Manager.Firewall()` lets host routes in front of the Manager use the same peer check.

## Sandbox

Every instance's JS hooks and migrations run in a sandbox (the [sandbox](../sandbox/README.md) plugin), and its SQL runs behind
SQLite's authorizer ([`sandbox/sqlguard.go`](../sandbox/sqlguard.go)). The sandbox plugin also owns the egress policy
([`sandbox/egress.go`](../sandbox/egress.go)); pocker turns it on for its instances. [`sandbox_test.go`](sandbox_test.go) runs one attack per
route from one instance against another instance and the host; all of these are refused:

| Area | What the sandbox does |
|---|---|
| Files: `$os.*`, `$filepath.glob/walk*`, `$filesystem.fileFromPath`, `$template.loadFiles`, `$apis.static`, `require()` | Confined to the instance folder with an `os.Root`: the kernel resolves every path beneath it, refusing `..` and symlinks that leave it. Relative paths and absolute paths inside the folder (`__hooks + "/x"`) work as before. |
| Host primitives: `$os.cmd`, `$os.exec`, `$os.exit`, `$os.args`, `$filesystem.local` | Disabled (`$os.args` is empty). |
| Environment: `$os.getenv`, `process.env` | Only the instance's own `secrets`. |
| Go internals: `$app.restart/bootstrap/runAllMigrations/onServe/onTerminate/onBootstrap/cron/unsafeWithoutHooks`, `hook.bindFunc/bind/trigger/unbind*`, `*sql.DB/Conn/Tx/Stmt` (e.g. `dbx.DB.DB()`), `database/sql/driver`, `os/exec`, `net`, `syscall`, `http.Server` controls | Hidden from JS (the field-name mapper maps them to nothing). Top-level `onRecord*`, `routerAdd`, `cronAdd` and the rest of `$app` work as usual. |
| Raw SQL (`$app.db().newQuery`) | Allowed. SQLite's authorizer refuses `ATTACH` (and `VACUUM INTO`) outside the instance's `pb_data`, `PRAGMA temp_store_directory/data_store_directory` and `load_extension`; the CLI-only `readfile`/`writefile`/`fsdir` don't exist in the library. Statements without a deadline get `max_query_duration`. |
| CPU | Every JS call is interrupted after `max_js_execution`; `sleep()` is capped at 10 s. |
| Memory, one shot | Built-ins that allocate a large result in one step refuse results over `max_js_allocation`: `String.repeat/padStart/padEnd`, `Array.join/toString`, `ArrayBuffer`, typed arrays (incl. array-likes, `.from`, and reaching the constructor through an instance), `Buffer.alloc*` / `new Buffer`, `$os.readFile`, `fileFromPath`, `readerToString`/`toBytes` limits. Recursion depth is capped (a JS stack overflow would otherwise crash the Go process). |
| Memory, gradual | Not limited. goja has no per-VM memory accounting, so a hook's gradual growth counts against the whole process; the process can use the host's swap, and the OS is the last line. (A memory watch with strikes and quarantine was removed on 2026-11-14: it could fail requests where swap would have absorbed them, and it never fired in production.) |
| Outbound network: `$http.send`, `$filesystem.fileFromURL`, S3 storage and backups, OAuth2/JWK fetches, SMTP | Public addresses only, checked on the address actually connected to (after DNS, so rebinding doesn't help): loopback, private ranges, link-local and cloud metadata, CGNAT, multicast, reserved and documentation ranges, and IPv6 equivalents/IPv4-in-IPv6 are refused unless listed in `egress_allow`. Installed process-wide on Go's default HTTP transport and PocketBase's SMTP client (`pocker/egress.go`). Host code that must reach private addresses (e.g. a control plane over a private network) uses its own `http.Client` with its own transport. `$http.send` timeouts and response bodies are capped. |

**Limits of the memory controls:** only one-shot allocations are capped. A hook that grows memory gradually slows or,
past swap, restarts the whole process (every instance in it) rather than failing alone; per-instance memory accounting
needs a goja with a memory API, or one process per instance (the docker provider).

**Not covered yet:** per-instance concurrency limits, and OS-level defence in depth: run Pocker as a non-root user with an
OS firewall that only lets that user reach public addresses (a second, independent egress layer), plus seccomp and
Landlock.

## Changes to PocketBase

These live in the repo root (PocketBase's own files), not in this plugin.

Kept small and marked `// PocketOne:` so upstream merges stay easy:

- `core.BaseAppConfig.AppMigrations`: a per-app migrations list (JS migrations from `plugins/jsvm` register there
  instead of the global `core.AppMigrations`).
- `core.BaseAppConfig.EncryptionKey`: the settings encryption key passed in directly instead of read from the process
  environment.
- `plugins/jsvm`: `Config.Extensions` ([`extension.go`](../../plugins/jsvm/extension.go)): values with optional
  methods (`OnInit`, `OnCall` around every call into JS, `HTTPClient` for `$http.send`, `HookFiles` /
  `MigrationFiles` / `Require`, `OnLoad` / `OnCompile` for error mapping), plus `Run` and `ScriptPath`. The
  [sandbox](../sandbox/README.md), [jsembed](../jsembed/README.md) and [jserrors](../jserrors/README.md) are
  extensions.
- Mail needs no core change: the sandbox sends an app's own SMTP through the egress policy on `OnMailerSend`
  (`sandbox.BindMail`).

## Versions

PocketOne has its own version line, starting at `v0.0.1`, independent of PocketBase's numbers. When PocketBase
releases, we merge it and decide then how to bump ours. Each release says which PocketBase version it's based on.

The module path stays `ganigeorgiev.com/pocketbase/pocketbase`, so a build pins a PocketOne version through the
`replace` line:

```
replace ganigeorgiev.com/pocketbase/pocketbase => github.com/pockethost/pocketone v0.0.1
```

## Upstream tracking

Fetch upstream without its tags: PocketBase's `v0.x` tags must never land in this repo, or Go would take `v0.41.1` for
the newest PocketOne.

```sh
git remote add upstream https://ganigeorgiev.com/git/pocketbase/pocketbase
git config remote.upstream.tagOpt --no-tags
git fetch upstream refs/tags/v0.41.2   # one release, without creating the tag here
git merge FETCH_HEAD   # resolve conflicts in the PocketOne-marked spots, rerun `go test ./core/... ./plugins/jsvm/...` and `go test ./...` in `ext/pocker` and `ext/sandbox`
```
