# Pocker

Pocker hosts many PocketBase instances in one process. It is PocketBase itself (this repo tracks
[upstream PocketBase](https://ganigeorgiev.com/git/pocketbase/pocketbase) and shares its history) plus a small engine in
[`pocker/`](pocker) and a CLI in [`cmd/pocker/`](cmd/pocker).

- Requests are routed by `Host` to an instance.
- An instance's app starts on its first request (tens of milliseconds) and keeps running.
- Each instance has its own folder (`pb_data`, `pb_hooks`, `pb_migrations`, `pb_public`), its own environment for
  JS hooks (`$os.getenv`, `process.env`), its own JS migrations and its own settings encryption key.
- Instance information comes from a provider ([`InstanceSource`](pocker/instance.go)). The built-in one reads a YAML
  file and reloads it when it changes; hosting platforms plug in their own.

## Quick start

```sh
go build -o pocker ./cmd/pocker

cat > instances.yaml <<'EOF'
instances:
  - id: blog
    hosts: [blog.localhost]
  - id: shop
    hosts: [shop.localhost]
    env: { STRIPE_KEY: sk_test_123, PB_ENCRYPTION_KEY: 0123456789abcdef0123456789abcdef }
EOF

./pocker serve            # listens on :8090, data in ./pocker_data/<id>
curl -H 'Host: blog.localhost' http://127.0.0.1:8090/api/health
```

Optional `pocker.yaml` (every field has a default):

```yaml
listen: ":8090"
data_root: "./pocker_data"
instances_file: "./instances.yaml"
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
