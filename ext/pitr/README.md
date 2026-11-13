# pitr

Point-in-time restore: Litestream replication of each app's database (local or a bucket) and in-place restore from the Backups screen.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **Status.** Working: Go module `github.com/pockethost/pocketone/ext/pitr` (Litestream v0.5.18, pinned).
> [`example/main.go`](example/main.go) runs one app with local history.

## What it does

On by default: including the plugin is the opt-in. `enabled: false` turns it off for the server or for one
instance.


- Litestream continuously copies the app's `data.db` (records, collections, settings, superusers) to
  `pb_data/litestream/`, or to an S3-compatible bucket.
- **Settings → Backups** gets a "Point-in-time restore" section: the restorable window, a time picker, **Restore**,
  and **Undo this restore**. Superusers only.
- A restore is built into a side file while the app keeps serving, then swapped in when the app restarts. The
  replaced database is kept in `pb_data/litestream/before-restore.db`, and history keeps recording, so the restore
  can be undone.
- Not covered: uploaded files, `pb_hooks`, `pb_public`, the logs database.
- `pb_data/litestream/` is left out of backup zips, and restoring a zip doesn't wipe it.

## Single app

```go
app := pocketbase.New()
pitr.MustRegister(app, pitr.Config{Retention: 72 * time.Hour})
app.Start()
```

Commands:

```
pocketbase pitr status
pocketbase pitr restore --at 2026-11-11T14:05:00Z --to /tmp/then.db   # side restore, any time
pocketbase pitr restore --at 2026-11-11T14:05:00Z                     # in place, swapped in on the next start
```

Replication runs only while the app serves (`OnServe`), never for CLI commands.

## Many apps (pocker)

A pocker plugin (`Configurer`, `AppIniter`, `Starter`):

```go
srv := &pocker.Server{Config: cfg, Plugins: []pocker.Plugin{pitr.New(pitr.Config{})}}
```

```yaml
# pocker.yaml (fields set in Go win)
pitr:
  retention: 72h          # on by default; enabled: false turns it off for every instance
  snapshot_interval: 24h
  bucket: { endpoint: ..., bucket: ..., region: auto, access_key: ..., secret: ..., label: EU }

# instances.yaml (or a provider): only enabled and retention per instance (e.g. enabled: false for a plan without PITR)
instances:
  - id: abc
    hosts: [abc.example.com]
    plugins: { pitr: { retention: 168h } }
```

- Each instance's history goes to `instances/<id>/litestream/` in the bucket (or its own `pb_data/litestream/`).
  A `bucket` in instance settings is refused, so credentials never sit in instance data.
- Sleep and wake are covered by the app's own `OnServe`/`OnTerminate`: history is attached when an instance serves,
  flushed and detached when it stops. Sleeping instances cost nothing.
- Restores restart only that instance (pocker turns `app.Restart()` into a restart of that one app).
- Apps with different retention get separate Litestream stores (Litestream applies retention per store).
- `Plugin.Status` and `Plugin.Restore` are there for host code.

## Bucket

```go
Bucket: &pitr.Bucket{
    Endpoint: "https://<account>.eu.r2.cloudflarestorage.com",
    Bucket:   "pitr-eu",
    Region:   "auto",
    AccessKey: "...", Secret: "...",
    Prefix:   "",      // keys: <prefix><AppConfig.Key>litestream/...
    Label:    "EU",    // shown in the admin: "stored on bucket (EU)"
},
```

Uses PocketBase's own S3 client (no AWS SDK). Each LTX file's timestamp is kept in object metadata, so restoring to
a time doesn't depend on upload times. Credentials stay in the host process. Residency is the operator's: give every
server in a region that region's bucket.

## Cost

Per serving app: about 176 KiB of memory, 4 goroutines, 5 file descriptors. Binary: about +4 MiB.

## License

MIT
