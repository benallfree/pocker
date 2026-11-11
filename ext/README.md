# ext: PockerBase plugins

Each folder here will be one plugin, with its own Go module (`github.com/pockerbase/pocketbase/ext/<name>`) and version tags (`ext/<name>/v0.1.0`), so heavy dependencies (Litestream, CertMagic) never reach core or other plugins. This folder is ours, separate from upstream's `plugins/`, so upstream merges never touch it.

> **Planned, nothing here yet.** The plugin API is described in [docs/plugins.md](../docs/plugins.md).

| Plugin | What it will do | Runs on |
|---|---|---|
| [`multitenant`](multitenant/README.md) | The engine: many PocketBase apps in one process, routed by hostname, each started on its first request. Instances come from a YAML file or any provider. | Multitenant host (the other host-level plugins register on it) |
| [`sandbox`](sandbox/README.md) | The JS sandbox for untrusted instances: file access confined to the instance folder, host primitives removed, CPU and memory caps, SQLite authorizer, public-only outbound network. | Any app; required by multitenant for untrusted tenants |
| [`pitr`](pitr/README.md) | Point-in-time restore: Litestream replication of each app's database (local or a bucket) and in-place restore from the Backups screen. | Single app or multitenant |
| [`bulkmail`](bulkmail/README.md) | Bulk mail: campaigns to an app's own users with one-click unsubscribe, bounce and complaint handling, and a dashboard page. | Single app or multitenant |
| [`pages`](pages/README.md) | Server-rendered multi-page apps from a `pb_pages` folder, PocketPages-compatible, with routing and templates in Go. | Single app or multitenant |
| [`storage`](storage/README.md) | A default storage and backups backend per app (for example an operator bucket) when the app's own S3 is off. Credentials never reach the app. | Single app or multitenant |
| [`s3redirect`](s3redirect/README.md) | File downloads as short-lived signed redirects, after PocketBase's own access check. | Single app or multitenant |
| [`mothership`](mothership/README.md) | A mothership and its edges: the control plane (instances, edges, regions, admin screens) and the edge side that follows it, each edge owning its own instances. | Requires multitenant |
| [`vacuum`](vacuum/README.md) | Scheduled database compaction: online VACUUM for a single app; under multitenant, one stopped instance at a time. | Single app or multitenant |
| [`deploy`](deploy/README.md) | A deploy API: upload code sets (hooks, migrations, public files), swap them in safely, stream logs. Pluggable auth. | Single app or multitenant |
| [`firewall`](firewall/README.md) | Peer allow-lists, trusted proxies and per-IP rate limits; under multitenant, per-instance and cross-instance limits too. | Single app or multitenant |
| [`autotls`](autotls/README.md) | Automatic certificates through ACME: on demand for every served hostname, wildcards through DNS-01, more CAs. | Single app or multitenant |
| [`servertiming`](servertiming/README.md) | `Server-Timing` headers with timings for each step of a request (and each hop under multitenant). | Single app or multitenant |
| [`schedules`](schedules/README.md) | Scheduled webhooks per instance, called in-process even while the instance sleeps, with a catch-up run after downtime. | Multitenant |

All MIT, like PocketBase.
