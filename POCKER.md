# Pocker

Pocker hosts many PocketBase instances in one process. It is PocketBase itself (this repo tracks
[upstream PocketBase](https://ganigeorgiev.com/git/pocketbase/pocketbase) and shares its history) plus a small engine in
[`pocker/`](pocker) and a CLI in [`cmd/pocker/`](cmd/pocker).

- One port, many instances. A request goes to the instance named by the `X-PocketBase-Id` header (from a trusted
  proxy only) or else by its hostname.
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

The instance id is the hostname (or `X-PocketBase-Id`), its folder is created on first use, and apps run in
PocketBase dev mode.

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
`X-PocketBase-Id` is honoured only from `trusted_proxies` (default loopback) and stripped from every request. Hosting
platforms plug in their own [`InstanceSource`](pocker/instance.go) (for example a control plane streaming changes over
SSE) instead of the file.

Optional `pocker.yaml` (every field has a default):

```yaml
listen: ":8090"
pb_root: "./pb_root"
dev: false
instances_file: "./instances.yaml"
trusted_proxies: ["127.0.0.1/32", "::1/128"]
hooks_pool_size: 2
shutdown_timeout: 10s
```

`GET /_pocker/health` on any host reports `{status, instances, running}`.

## Security status

**Not a sandbox yet.** Every instance runs in the same process with the stock JS hooks layer, so an instance's hooks can
read other instances' files, the host environment through other bindings, or run commands. Only run instances you trust
until the locked-down `plugins/jsvm` lands.

## Changes to PocketBase

Kept small and marked `// POCKER:` so upstream merges stay easy:

- `core.BaseAppConfig.AppMigrations`: a per-app migrations list (JS migrations from `plugins/jsvm` register there
  instead of the global `core.AppMigrations`).
- `core.BaseAppConfig.EncryptionKey`: the settings encryption key passed in directly instead of read from the process
  environment.

## Upstream tracking

```sh
git remote add upstream https://ganigeorgiev.com/git/pocketbase/pocketbase
git fetch upstream --tags
git merge v0.41.2   # resolve conflicts in the POCKER-marked spots, rerun `go test ./pocker/... ./core/... ./plugins/jsvm/...`
```
