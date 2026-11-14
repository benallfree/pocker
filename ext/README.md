# ext: PocketOne plugins

Each folder here will be one plugin, with its own Go module (`github.com/pockethost/pocketone/ext/<name>`) and version tags (`ext/<name>/v0.1.0`), so heavy dependencies (Litestream, CertMagic) never reach core or other plugins. This folder is ours, separate from upstream's `plugins/`, so upstream merges never touch it.

> **Planned, nothing here yet.** The plugin API is described in [docs/plugins.md](../docs/plugins.md).

| Plugin | What it will do | Runs on |
|---|---|---|
| [`pocker`](pocker/README.md) | Many instances in one process (level 2), the engine: many PocketBase apps in one process, routed by hostname, each started on its first request. Instances come from a YAML file or any provider. | the multi-instance host; the other host-level plugins register on it |
| [`sandbox`](sandbox/README.md) | The JS sandbox for untrusted instances: file access confined to the instance folder, host primitives removed, CPU and memory caps, SQLite authorizer, public-only outbound network. | Any app; required by pocker for untrusted tenants |
| [`pitr`](pitr/README.md) | Point-in-time restore: Litestream replication of each app's database (local or a bucket) and in-place restore from the Backups screen. | Single app, or many under pocker |
| [`bulkmail`](bulkmail/README.md) | Bulk mail: campaigns to an app's own users (audiences from a collection, a view or SQL) with one-click unsubscribe, bounce and complaint handling, and a dashboard page. In the standard binary. | Single app, or many under pocker |
| [`mailrelay`](mailrelay/README.md) | Outgoing mail for instances without their own SMTP: sent through the operator's relay as `<instance>@<domain>` with per-instance limits, instead of the host's `sendmail`. In dev mode it relays into the dev mail trap ([`mailtrap`](mailtrap/README.md)). | pocker |
| [`mcp`](mcp/README.md) | An MCP endpoint at `/mcp` for agents (Claude connectors, Claude Code): built-in `pb_*` tools, tools from other plugins and `$mcp.addTool`, OAuth discovery for sign-in through `oauth-server`. | Single app, or many under pocker |
| [`pages`](pages/README.md) | Server-rendered multi-page apps from a `pb_pages` folder, PocketPages-compatible, with routing and templates in Go. | Single app, or many under pocker |
| [`storage`](storage/README.md) | A default storage and backups backend per app (for example an operator bucket) when the app's own S3 is off. Credentials never reach the app. | Single app, or many under pocker |
| [`s3redirect`](s3redirect/README.md) | File downloads as short-lived signed redirects, after PocketBase's own access check. On by default in the standard binary. | Single app, or many under pocker |
| [`mothership`](mothership/README.md) | A mothership and its edges: the control plane (instances, edges, regions, admin screens) and the edge side that follows it, each edge owning its own instances. | Requires [pocker](../pocker/README.md) |
| [`tenants`](tenants/README.md) | Accounts that own instances: quotas, owner logins, tokens, the API for your own tenant dashboard (level 4). | Requires mothership and sandbox |
| [`tenant-billing-ls`](tenant-billing-ls/README.md) | Lemon Squeezy billing for tenants: checkout, webhooks, quotas, reconcile. | Requires tenants |
| [`vacuum`](vacuum/README.md) | Scheduled database compaction: online VACUUM for a single app; under pocker, one stopped instance at a time. | Single app, or many under pocker |
| [`tokens`](tokens/README.md) | API tokens (`pbt_…`) for any auth collection: bearer auth, scopes that plugins register, revoke one or all, Settings → API tokens, `pocketbase token`. | Single app, or many under pocker |
| [`oauth-server`](oauth-server/README.md) | OAuth 2.1 authorization server issuing API tokens: sign-in + consent pages (brandable), code + PKCE, refresh, device codes, revocation, dynamic client registration; `pocketbase login` and MCP connectors use it. | Single app; requires tokens |
| [`remotes`](remotes/README.md) | Named remotes for the `pocketone` command (`remote`, `login`, `logout`, `link`), saved logins, and `GET /_pocker/whoami` for discovery. | The `pocketbase` command; any app (whoami) |
| [`deploy`](deploy/README.md) | Deploys: `pocketone deploy | dev | pull | logs` (also to PocketHost edges) and the deploy API (hash diff, safe swap, history, logs), with pluggable auth. | The `pocketbase` command; single app, or many under pocker with your own backend |
| [`config`](config/README.md) | `pocketone.yaml` with sections owned by plugins (typed keys; read only through the owner's key) and a private `.env` for secrets; `pocketbase config`. | Every binary (stock in `pocketbase`) |
| [`docker`](docker/README.md) | Runtime provider for a host: each instance in its own container with an official PocketBase or PocketOne binary of its version (`pb:`/`ce:`), idle stop, adoption across restarts. | Host (one provider per process), Docker |
| [`host`](host/README.md) | The multi-instance host: instances by hostname, the request front, listeners/TLS, gates, and the runtime contract; exactly one provider (pocker or docker). | Many-instance binaries |
| [`lifecycle`](lifecycle/README.md) | Instance data on a host: facts (disk used, last request served) reported to a sink, and decisions from a policy provider (local YAML rules, or a control plane's). First step: measuring. | Any host |
| [`firewall`](firewall/README.md) | Peer allow-lists, trusted proxies and per-IP and per-instance rate limits for a host; turns on with `firewall:` / `rate_limits:`. | Any host, with or without edge |
| [`edge`](edge/README.md) | The fleet layer: forwarding to the owner edge, edges from YAML or a mothership. | On a host (pocker or docker) |
| [`autotls`](autotls/README.md) | Automatic certificates through ACME: on demand for every served hostname, wildcards through DNS-01, more CAs. | Single app, or many under pocker |
| [`servertiming`](servertiming/README.md) | `Server-Timing` headers with timings for each step of a request (and each hop under pocker). | Single app, or many under pocker |
| [`jserrors`](jserrors/README.md) | JS hook errors name `pb_hooks/<file>:<line>:<col>` instead of `<cwd>/pb.js:1:…` (handlers, middlewares, cron and hooks-file loads). | Single app, or many under pocker |
| [`jsembed`](jsembed/README.md) | JS hooks and migrations from an `fs.FS` (e.g. embedded in the binary) instead of `pb_hooks`/`pb_migrations`, with `require()` sealed to the embedded files. | Single app |
| [`mailtrap`](mailtrap/README.md) | Dev mail catcher: in dev mode, an app with SMTP off sends to an inbox at http://127.0.0.1:8025 instead of the host's `sendmail`; under pocker, one inbox for every dev instance. | Single app, or many under pocker |
| [`devusers`](devusers/devusers.go) | Local testing only: on bootstrap, upserts superuser admin@example.com and user user@example.com (verified, in `users`), both with password `password`. Every plugin's `example/` registers it; production builds never do. | Example and test builds |
| [`schedules`](schedules/README.md) | Scheduled webhooks per instance, called in-process even while the instance sleeps, with a catch-up run after downtime. | pocker |

All MIT, like PocketBase.
