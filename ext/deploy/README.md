# deploy

Deploy a project's code to a PocketBase remote, and accept deploys: pb_hooks, pb_migrations, pb_public, packages
and the production node_modules tree, sent as a hash diff and swapped in safely.

```
pocketbase link https://myapp.example.com     # ext/remotes: add the remote and sign in
pocketbase deploy                              # upload what changed; the remote restarts when hooks changed
pocketbase logs
```

**Runs on:** the `pocketone` command (client) and a single app (server). PocketHost runs the same server code under
pocker with its own backend.

```go
app := pocketbase.New()
tokens.MustRegister(app, tokens.Config{})   // optional: API tokens with the deploy scope
remotes.MustRegister(app, remotes.Config{}) // whoami discovery + login
deploy.MustRegister(app, deploy.Config{})   // the deploy API + deploy/dev/pull/logs commands
```

Try it: see [`example/main.go`](example/main.go) (logins from [devusers](../devusers/devusers.go): admin@example.com /
password).

## Client: `deploy`, `dev`, `pull`, `logs`

- `deploy [remote]`: `--dry-run`, `--out <dir>` (write the set to a folder), `-i/-e` (more include/exclude patterns),
  `--commit`, `-m`, `--node-modules` / `--no-node-modules`, `-v`.
- `dev [remote]`: deploy now, then on every change (debounced), streaming the remote's logs (`--no-logs`).
- `pull [remote] [paths…]`: download files into the project (default `pb_hooks`, `pb_migrations`, `pb_public`,
  `package.json`; `pb_data` only when named, and only while the remote app is stopped).
- `logs [remote] [-n 100]`: replay, then follow.

The deploy set: files matching `pb_*`, `pb_*/**/*`, `package.json`, `bun.lock(b)`, `patches/**` except
`pb_data/**`, dotfiles included (`pb_public/.well-known`), `.git*` and symlinks skipped. With a `package.json`, the
production `node_modules` tree is collected the way Node resolves it (hoisted npm/bun, pnpm's store, workspace links;
never devDependencies) and shipped as plain files, so plain JS hooks can `require` npm packages; the remote then
owns its root `node_modules` (packages no longer shipped are removed). The collector is
[`nodemodules`](nodemodules/nodemodules.go), a port of the npm CLI's.

The client finds the API through `/_pocker/whoami` (`/_pocker/deploy/v1` on PocketOne remotes) and falls back to
`/_ph/deploy/v1`, so it deploys to PocketHost edges with a `ph_pat_` token today (`POCKETHOST_TOKEN`, or the
`pockethost` remote read from `.pockethostconfig`).

## Server: the deploy API

`/_pocker/deploy/v1`: `GET /whoami`, `POST /check` (manifest → missing hashes), `POST /blobs` (gzipped `PHB1`
bundles: `[sha256][uint32 length][bytes]`…, every hash verified), `POST /commit` (stage, swap in, record, restart),
`GET|DELETE /files`, `GET|PUT /files/raw`, `GET /logs` (server-sent events). The protocol is the npm CLI's.

On a single app ([`Register`](app.go)):
- **Who may deploy:** superusers. A superuser JWT (including an impersonate token) has full access; an API token
  needs the `deploy` scope (or no scopes), and `deploy:read` allows whoami, files and logs only. The scopes are
  registered with [tokens](../tokens/README.md) when it's present.
- **Where:** `Config.Dir`, default the parent of `pb_data` (PocketBase's default layout). Blobs and past manifests
  live in `<Dir>/.pb_deploy` (not backed up, never deployed).
- **Restart:** a deploy that changed anything outside `pb_public` restarts the app (`app.Restart()` re-executes the
  process a second after the response; `Config.Restart` overrides it). The files are swapped in while the app runs,
  each rename atomic.
- **History:** `_deploys` (status, counts, bytes, commit, message, source, token name, superuser, error).
- **Logs:** the app's own `_logs` (requests shortened to `GET /url → 200 (1.2 ms)`, errors as stderr). PocketBase
  writes logs in batches, so lines show up a few seconds late.

Elsewhere, build the API yourself: `deploy.New(deploy.Options{Prefix, Backend, Instances, PBRoot, DataRoot,
PlatformPaths, Logs})` and mount `api.Wrap(next)`. `Backend` decides which instance a request is for
(`Lookup(r)`), who it acts as (`Authenticate(r, inst)` → `Auth{UserID, Scopes: read/write, Via, TokenLabel}`,
`*AuthError`, or `ErrNoAccess` for a 404) and records history; `Instances` is pocker's Manager (stop, swap, start);
`FileLogs` tails pocker's `logs/exec.log`. PocketHost composes it with its PocketHost-token backend and keeps
`/_ph/deploy/v1`.

## License

MIT
