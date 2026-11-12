# storage

The operator's fall-through storage: named S3 backends, and mounts that send each filesystem kind (record uploads,
backup zips, PITR history, any plugin's own kind) to one of them, per instance. Credentials never reach the instance.

**Runs on:** many apps under [pocker](../pocker/README.md); a single app can use it too (`Bind(app, "")`).

> **Spike.** Works and is tested against MinIO; not yet deployed.

For instances created with the plugin on. Files already on an instance's local disk are not read or moved (legacy
instances are migrated separately).

## Setup

```go
srv := &pocker.Server{Config: cfg, Plugins: []pocker.Plugin{storage.New(storage.Config{})}}
```

```yaml
# pocker.yaml (fields set in Go win)
storage:
  backends:                        # named S3-compatible destinations
    r2-eu:   { endpoint: https://<account>.eu.r2.cloudflarestorage.com, bucket: pocketbase-eu, region: auto, access_key: ..., secret: ..., prefix: "" }
    b2-cold: { endpoint: ..., bucket: ph-backups, region: ..., access_key: ..., secret: ... }
  mounts:                          # filesystem kind -> backend
    storage:    r2-eu              # record uploads
    backups:    b2-cold            # backup zips
    litestream: r2-eu              # PITR history (ext/pitr)
  # enabled: false                 # off for every instance

# instances.yaml (or a provider): only enabled per instance
instances:
  - id: blog
    plugins: { storage: { enabled: false } }
```

Each app gets `<backend prefix>instances/<id>/<kind>/` per mounted kind (a single app bound with `Bind(app, "")`:
`<backend prefix><kind>/`) and can't reach anything outside it. Any kind a plugin asks core for
(`app.NewFilesystemOfKind(kind, ...)`) is served when it's mounted; uploads and backups step aside when the app turned
on its own S3. `Plugin.FS(instanceID, kind)` returns the same fenced filesystem outside a hook.

The old shape (`bucket:` + `uploads:` / `backups:`) still works, with a warning: it becomes backend `default` mounted
for storage and backups.

No mounts: the plugin does nothing; files stay on local disk as in PocketBase.

## How it works

- PocketBase asks the `OnFilesystemCreate` hook before it creates its uploads or backups filesystem. When the
  instance's own S3 is off, the plugin answers with the bucket, fenced to `<prefix>instances/<id>/storage/` (uploads,
  thumbs) and `<prefix>instances/<id>/backups/` (zips). An instance can't list, read, write or delete anything outside
  its folder.
- To the instance nothing is configured: Settings show S3 off, uploads, thumbs, `$app.newFilesystem()` and the
  Backups screen work as before. Without s3redirect, downloads stream through the server.
- With [`ext/s3redirect`](../s3redirect/README.md) on, downloads become signed redirects to the bucket. Nothing to
  wire: the fenced filesystem signs with the prefix, so s3redirect sees it like any S3 mount.
- When the instance turns on its own S3, PocketBase's normal code takes over with the instance's credentials. Files
  in the default bucket stay there (PocketBase's own S3 switch doesn't move files either).

## License

MIT
