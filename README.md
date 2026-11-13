# PocketOne - A community fork of PocketBase

PocketOne is a community fork of [PocketBase](https://pocketbase.io). It tracks
upstream and aims to keep its own changes to the core small: just the hooks that let plugins extend and override
PocketBase. Everything else is a plugin. Maintained by [PocketHost](https://pockethost.io); not affiliated with or
endorsed by the PocketBase project.

## Install

```bash
brew install pockethost/tap/pocketone
```

That's [Homebrew](https://brew.sh) on macOS or Linux (the formula lives in
[pockethost/homebrew-tap](https://github.com/pockethost/homebrew-tap)). Or download the zip for your platform from
[Releases](https://github.com/pockethost/pocketone/releases). Then:

```bash
pocketone serve
```

Start a site from a starter kit with `pocketone pages create myapp` ([pages](ext/pages/README.md)).

In dev mode (`serve --dev`), an app without SMTP doesn't use the host's `sendmail`: its mail is caught in-process and
you read it at http://127.0.0.1:8025 ([mailtrap](ext/mailtrap/README.md)).

## Core Extensions

These are extensions to PocketOne's core that ship with every binary. They do not add runtime overhead if you
choose not to use them.

| Plugin | Description | Status |
|---|---|---|
| **[PITR](ext/pitr/README.md)** (Litestream) | Point-in-time restore of your database, from Settings → Backups or `pocketone pitr restore`. On by default; `--pitr=false` turns it off. | Stable |
| **[Auto TLS](ext/autotls/README.md)** | On-demand certificates and wildcards through ACME. | Stable under Pocker; Beta on a single app (`--autotls`) |
| **[Bulk Mail](ext/bulkmail/README.md)** | Campaigns to your app's own users, with audiences from a collection, a view or SQL. | Beta |
| **[Pages](ext/pages/README.md)** (formerly PocketPages) | Server-rendered pages from a `pb_pages` folder. | Beta |
| **[Vacuum](ext/vacuum/README.md)** | Scheduled SQLite VACUUM. | Planned |
| **[MCP](ext/mcp/README.md)** | An MCP endpoint at `/mcp` so agents can manage your app and call its tools. | Beta |
| **[OAuth Server](ext/oauth-server/README.md)** | Your app as an OAuth provider; `pocketone login` and MCP sign-in use it. | Beta |
| **[S3 Redirect](ext/s3redirect/README.md)** | File downloads as short-lived signed S3 redirects. | Beta |
| **[Tokens](ext/tokens/README.md)** | Long-lived API tokens (`pbt_…`) with scopes. | Beta |
| **[Deploy](ext/deploy/README.md)** | `pocketone deploy \| dev \| pull \| logs` and the deploy API. | Beta |
| **[Remotes](ext/remotes/README.md)** | Named remotes and saved logins for the `pocketone` command. | Beta |

## Community Plugins

These plugins are maintained by PocketHost and the community and are MIT. You can mix and match them to create your
own custom PocketOne experience.

**Run many instances**

| Plugin | Description | Status |
|---|---|---|
| **[Pocker](ext/pocker/README.md)** | Hundreds of PocketOne apps in one Go process, routed by hostname. | Stable |
| **[Schedules](ext/schedules/README.md)** | Scheduled webhooks that wake sleeping instances (headed into Pocker). | Planned |
| **[Mothership](ext/mothership/README.md)** | A control plane and its edges, across regions. Needs Pocker. | Planned |
| **[Sandbox](ext/sandbox/README.md)** | Confines untrusted JS hooks, SQL and outbound network ([how is this safe?](ext/sandbox/README.md#how-is-this-safe)). Required for Multitenant. | Stable |
| **[Host](ext/host/README.md)** | Many instances behind one listener, routed by hostname, run by one provider (Pocker or Docker). | Stable |
| **[Firewall](ext/firewall/README.md)** | Peer allow-lists, trusted proxies and rate limits for a host. | Stable |
| **[Edge](ext/edge/README.md)** | The fleet layer: forward to the edge that owns an instance. | Stable |

**Services for your instances**

| Plugin | Description | Status |
|---|---|---|
| **[Mail Relay](ext/mailrelay/README.md)** | Outgoing mail for instances without SMTP, sent as `<instance>@<your domain>` through your relay, with limits. | Alpha |
| **[S3 Fallback](ext/storage/README.md)** | A default bucket for uploads and backups when an instance hasn't set its own S3. | Alpha |
| **[Server Timing](ext/servertiming/README.md)** | `Server-Timing` headers for each step of a request. | Stable |

**Sell it as a service**

| Plugin | Description | Status |
|---|---|---|
| **[Multitenant](ext/tenants/README.md)** | Accounts that own instances: quotas, owner logins, tokens, and the API for your own dashboard. | Planned |
| **[Multitenant Billing](ext/tenant-billing/README.md)** | Plans, quotas and the billing provider hook for Multitenant. | Planned |
| **[Multitenant Billing LemonSqueezy Adapter](ext/tenant-billing-ls/README.md)** | Lemon Squeezy as the billing provider. | Planned |

**For plugin authors**

| Plugin | Description | Status |
|---|---|---|
| **[Mail Trap](ext/mailtrap/README.md)** | Dev mode: an app without SMTP sends to a built-in inbox at http://127.0.0.1:8025 instead of `sendmail`. | Beta |
| **[Dev Users](ext/devusers/devusers.go)** | Local testing only: a superuser and a user with password `password`. Every plugin's `example/` uses it. | Stable |

**Stable** runs in PocketHost production · **Beta** complete and tested, not yet in production · **Alpha** working
code, still changing · **Planned** a plan and a README so far.

## Contribute a plugin

Plugins are welcome. Read [Create your own plugin](docs/plugins.md), then open a pull request that adds
`ext/<your-plugin>/`. Write it against the plugin contract ([`ext/ceplugin`](ext/ceplugin/ceplugin.go)) and the same plugin works at
every level: on a single app and on every instance under pocker. Keep changes to PocketBase's own code to the minimum hook your plugin needs, and include a small
`example/main.go` that builds and shows the feature.

## Built on PocketBase

PocketOne is built on [PocketBase](https://pocketbase.io), an open source Go backend with an embedded SQLite database
and realtime subscriptions, files and users management, a superuser dashboard and a REST-ish API. PocketOne keeps
PocketBase's APIs, so [PocketBase's documentation](https://pocketbase.io/docs) applies to the core, and the official
PocketBase SDKs ([JavaScript](https://github.com/pocketbase/js-sdk), [Dart](https://github.com/pocketbase/dart-sdk))
work with PocketOne apps.

PocketOne isn't affiliated with or endorsed by PocketBase. Please don't take PocketOne questions or issues to the
PocketBase project.

## Use as a standalone app

Install with Homebrew (above), or download the zip for your platform from
[Releases](https://github.com/pockethost/pocketone/releases), extract it and run `./pocketone serve`.

The prebuilt executables are built from [`examples/ce`](examples/ce/main.go): PocketBase's `examples/base` plus the
plugins that ship by default. They include the JS VM, so you can extend your app with JavaScript (see
[Extend with JavaScript](https://pocketbase.io/docs/js-overview/)), and the [Core Extensions](#core-extensions) above.

## Use as a Go framework

PocketOne keeps PocketBase's Go module path, so an app written against PocketBase builds against PocketOne with one
`replace` line in its `go.mod`:

```
replace ganigeorgiev.com/pocketbase/pocketbase => github.com/pockethost/pocketone v0.0.1
```

Plugins are separate modules under `github.com/pockethost/pocketone/ext/...`. See
[Create your own plugin](docs/plugins.md) for how to wire them in, and
[Extend with Go](https://pocketbase.io/docs/go-overview/) for the core APIs.

## Build from source

0. [Install Go 1.27+](https://go.dev/doc/install).
1. Clone this repo and go to `examples/ce`.
2. Run `CGO_ENABLED=0 go build -o pocketone` (for another platform, add `GOOS=linux GOARCH=amd64`).
3. Start it with `./pocketone serve`.

`examples/base` builds plain PocketBase without the plugins. The pure Go SQLite driver supports darwin, linux and
windows on amd64 and arm64, plus the other targets listed in
[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite#hdr-Supported_platforms_and_architectures).

## Testing

Run `go test ./...` at the repo root for the core, and inside each `ext/<name>` module for its plugin.

## Security

If you find a security issue in PocketOne, don't open a public issue. Contact the maintainers privately on
[Discord](https://discord.gg/nVTxCMEcGT).

## License

MIT. PocketOne includes PocketBase, copyright Gani Georgiev, under the MIT License; see [LICENSE.md](LICENSE.md).
