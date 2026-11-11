# ext: PocketBase CE plugins

Each folder here will be one plugin, with its own Go module (`github.com/pockethost/pocketbase/ext/<name>`) and version tags (`ext/<name>/v0.1.0`), so heavy dependencies (Litestream, CertMagic) never reach core or other plugins. This folder is ours, separate from upstream's `plugins/`, so upstream merges never touch it.

> **Planned, nothing here yet.** The plugin API is described in [docs/plugins.md](../docs/plugins.md).

| Plugin | What it will do | Runs on |
|---|---|---|
| [`pocker`](pocker/README.md) | Many instances in one process (level 2), the engine: many PocketBase apps in one process, routed by hostname, each started on its first request. Instances come from a YAML file or any provider. | the multi-instance host; the other host-level plugins register on it |
| [`sandbox`](sandbox/README.md) | The JS sandbox for untrusted instances: file access confined to the instance folder, host primitives removed, CPU and memory caps, SQLite authorizer, public-only outbound network. | Any app; required by pocker for untrusted tenants |
| [`pitr`](pitr/README.md) | Point-in-time restore: Litestream replication of each app's database (local or a bucket) and in-place restore from the Backups screen. | Single app, or many under pocker |
| [`bulkmail`](bulkmail/README.md) | Bulk mail: campaigns to an app's own users (audiences from a collection, a view or SQL) with one-click unsubscribe, bounce and complaint handling, and a dashboard page. In the standard binary. | Single app, or many under pocker |
| [`mailrelay`](mailrelay/README.md) | Outgoing mail for instances without their own SMTP: sent through the operator's relay as `<instance>@<domain>` with per-instance limits, instead of the host's `sendmail`. In dev mode it relays into PocketBase CE's built-in dev mail trap. | pocker |
| [`pages`](pages/README.md) | Server-rendered multi-page apps from a `pb_pages` folder, PocketPages-compatible, with routing and templates in Go. | Single app, or many under pocker |
| [`storage`](storage/README.md) | A default storage and backups backend per app (for example an operator bucket) when the app's own S3 is off. Credentials never reach the app. | Single app, or many under pocker |
| [`s3redirect`](s3redirect/README.md) | File downloads as short-lived signed redirects, after PocketBase's own access check. On by default in the standard binary. | Single app, or many under pocker |
| [`mothership`](mothership/README.md) | A mothership and its edges: the control plane (instances, edges, regions, admin screens) and the edge side that follows it, each edge owning its own instances. | Requires [pocker](../pocker/README.md) |
| [`tenants`](tenants/README.md) | Accounts that own instances: quotas, owner logins, tokens, the API for your own tenant dashboard (level 4). | Requires mothership and sandbox |
| [`tenant-billing-ls`](tenant-billing-ls/README.md) | Lemon Squeezy billing for tenants: checkout, webhooks, quotas, reconcile. | Requires tenants |
| [`vacuum`](vacuum/README.md) | Scheduled database compaction: online VACUUM for a single app; under pocker, one stopped instance at a time. | Single app, or many under pocker |
| [`tokens`](tokens/README.md) | API tokens (`pbt_…`) for any auth collection: bearer auth, scopes that plugins register, revoke one or all, Settings → API tokens, `pocketbase token`. | Single app, or many under pocker |
| [`oauth-server`](oauth-server/README.md) | OAuth 2.1 authorization server issuing API tokens: sign-in + consent pages (brandable), code + PKCE, refresh, device codes, revocation, dynamic client registration; `pocketbase login` and MCP connectors use it. | Single app; requires tokens |
| [`remotes`](remotes/README.md) | Named remotes for the `pocketbase` command (`remote`, `login`, `logout`, `link`), saved logins, and `GET /_pocker/whoami` for discovery. | The `pocketbase` command; any app (whoami) |
| [`deploy`](deploy/README.md) | Deploys: `pocketbase deploy | dev | pull | logs` (also to PocketHost edges) and the deploy API (hash diff, safe swap, history, logs), with pluggable auth. | The `pocketbase` command; single app, or many under pocker with your own backend |
| [`firewall`](firewall/README.md) | Peer allow-lists, trusted proxies and per-IP rate limits; under pocker, per-instance and cross-instance limits too. | Single app, or many under pocker |
| [`autotls`](autotls/README.md) | Automatic certificates through ACME: on demand for every served hostname, wildcards through DNS-01, more CAs. | Single app, or many under pocker |
| [`servertiming`](servertiming/README.md) | `Server-Timing` headers with timings for each step of a request (and each hop under pocker). | Single app, or many under pocker |
| [`devusers`](devusers/devusers.go) | Local testing only: on bootstrap, upserts superuser admin@example.com and user user@example.com (verified, in `users`), both with password `password`. Every plugin's `example/` registers it; production builds never do. | Example and test builds |
| [`schedules`](schedules/README.md) | Scheduled webhooks per instance, called in-process even while the instance sleeps, with a catch-up run after downtime. | pocker |

All MIT, like PocketBase.
