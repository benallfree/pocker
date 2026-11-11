# storage

A default bucket for record uploads and backup zips, per instance, when the instance hasn't turned on its own S3.
Credentials never reach the instance.

**Runs on:** many apps under [pocker](../pocker/README.md). (A single app has no use for it: its owner sets S3 in
Settings.)

> **Spike.** Works and is tested against MinIO; not yet deployed. Lazy move of existing files is on read only; the
> background sweep that moves the rest is still to do.

## Setup

```go
srv := &pocker.Server{Config: cfg, Plugins: []pocker.Plugin{storage.New(storage.Config{})}}
```

```yaml
# pocker.yaml (fields set in Go win)
storage:
  bucket:
    endpoint: https://<account>.eu.r2.cloudflarestorage.com
    bucket: pocketbase-eu
    region: auto
    access_key: ...
    secret: ...
    prefix: ""             # prepended to every key
  # enabled: false         # off for every instance
  # uploads: false         # leave record uploads on local disk
  # backups: false         # leave backup zips on local disk

# instances.yaml (or a provider): only enabled per instance
instances:
  - id: blog
    plugins: { storage: { enabled: false } }
```

No bucket: the plugin does nothing; files stay on local disk as in PocketBase.

## How it works

- PocketBase asks the `OnFilesystemCreate` hook before it creates its uploads or backups filesystem. When the
  instance's own S3 is off, the plugin answers with the bucket, fenced to `<prefix>instances/<id>/storage/` (uploads,
  thumbs) and `<prefix>instances/<id>/backups/` (zips). An instance can't list, read, write or delete anything outside
  its folder.
- To the instance nothing is configured: Settings show S3 off, uploads, thumbs, `$app.newFilesystem()` and the
  Backups screen work as before. Downloads stream through the server.
- Files already on local disk stay readable: the bucket is tried first, then `pb_data/storage` (or
  `pb_data/backups`). A file read from local disk is copied to the bucket and removed locally in the background
  (lazy move, no cutover). Deletes remove both copies; listings merge both.
- When the instance turns on its own S3, PocketBase's normal code takes over with the instance's credentials. Files
  in the default bucket stay there (PocketBase's own S3 switch doesn't move files either).

## License

MIT
