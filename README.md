# PocketBase Community Edition

PocketBase Community Edition (PocketBase CE) is a community fork of [PocketBase](https://pocketbase.io). It tracks
upstream and aims to keep its own changes to the core small: just the hooks that let plugins extend and override
PocketBase. Everything else is a plugin. Maintained by [PocketHost](https://pockethost.io); not affiliated with or
endorsed by the PocketBase project.

## Install

```bash
brew install pockethost/tap/pocketbase
```

That's [Homebrew](https://brew.sh) on macOS or Linux (the formula lives in
[pockethost/homebrew-tap](https://github.com/pockethost/homebrew-tap)). Or download the zip for your platform from
[Releases](https://github.com/pockethost/pocketbase/releases). Then:

```bash
pocketbase serve
```

Start a site from a starter kit with `pocketbase pages create myapp` ([pages](ext/pages/README.md)).

## Core features

What PocketBase CE adds to PocketBase itself, with no plugin. Each change is small and marked `// POCKER:` in the code,
so upstream merges stay easy.

- **Dev mail trap.** In dev mode (`serve --dev`), an app without SMTP doesn't use the host's `sendmail`. Its mail is
  caught by a mail catcher in the same process, like MailDev or Mailpit: nothing is delivered, and you read it at
  http://127.0.0.1:8025 (SMTP on 127.0.0.1:1025 for other tools). A notice at startup says so. Under
  [pocker](ext/pocker/README.md) every instance shares one inbox, tagged by instance. `PB_MAILTRAP_HTTP` and
  `PB_MAILTRAP_SMTP` change the ports. Turn SMTP on and the app sends normally; outside dev mode nothing changes.
- **Several apps in one process.** Settings, the JS migrations list and the encryption key belong to each app instead
  of the process, so one Go process can host many apps (what [pocker](ext/pocker/README.md) builds on).
- **Hooks for plugins.** A per-call wrapper around every JS hook, route and cron (`jsvm.Config.OnCall`, for time and
  concurrency limits); `$http.send` caps per runtime or per process; a dialer for PocketBase's SMTP client
  (`mailer.DialContext`, for egress rules).

Everything else is a plugin (below). Without plugins, an app behaves like stock PocketBase plus the features above.

## Four ways to run it

Each level adds plugins on top of the one before; dependencies only point down.

1. **PocketBase CE.** One app. No plugins: it behaves like stock PocketBase, plus the [core features](#core-features).
2. **Many instances, one process** ([pocker](ext/pocker/README.md)). One operator runs hundreds of PocketBase
   instances in a single Go process, routed by hostname.
3. **Many edges and regions** ([mothership](ext/mothership/README.md)). Still one operator: a control plane and edges in
   several regions, each edge running its own instances.
4. **Many owners** ([tenants](ext/tenants/README.md)). Instances belong to separate accounts, with quotas, owner logins
   and tokens; you run your own dashboard for them (as [PocketHost](https://pockethost.io) does). Billing is a plugin
   too, e.g. [tenant-billing-ls](ext/tenant-billing-ls/README.md) for Lemon Squeezy.

Running code you don't control (other people's hooks) needs the [sandbox](ext/sandbox/README.md)
([how is this safe?](ext/sandbox/README.md#how-is-this-safe)): optional at levels 2 and 3, required at level 4.
Feature plugins work at any level.

## Plugins

| Plugin | Core | Pocker | Mothership | Multitenant | Status | What it does |
|---|:-:|:-:|:-:|:-:|---|---|
| [pocker](ext/pocker/README.md) | · | ✓ | ✓ | ✓ | 🟩🟩🟩 | Many PocketBase instances in one Go process, routed by hostname |
| [mothership](ext/mothership/README.md) | · | · | ✓ | ✓ | 🟥⬜⬜ | A control plane and its edges, across regions |
| [tenants](ext/tenants/README.md) | · | · | · | ✓ | 🟥⬜⬜ | Accounts that own instances: quotas, owner logins, tokens, the API for your dashboard |
| [tenant-billing-ls](ext/tenant-billing-ls/README.md) | · | · | · | ✓ | 🟥⬜⬜ | Billing tenants with Lemon Squeezy |
| [sandbox](ext/sandbox/README.md) | ✓ | ✓ | ✓ | ✓ | 🟩🟩🟩 | Confine untrusted JS hooks, SQL and outbound network ([how is this safe?](ext/sandbox/README.md#how-is-this-safe)) |
| [firewall](ext/firewall/README.md) | ✓ | ✓ | ✓ | ✓ | 🟩🟩🟩 | Peer allow-lists, trusted proxies and rate limits |
| [autotls](ext/autotls/README.md) | ✓ | ✓ | ✓ | ✓ | 🟩🟩🟩 | Automatic TLS: on-demand certificates and wildcards |
| [servertiming](ext/servertiming/README.md) | ✓ | ✓ | ✓ | ✓ | 🟩🟩🟩 | Server-Timing headers |
| [pitr](ext/pitr/README.md) | ✓ | ✓ | ✓ | ✓ | 🟧🟧⬜ | Point-in-time restore (Litestream) |
| [bulkmail](ext/bulkmail/README.md) | ✓ | ✓ | ✓ | ✓ | 🟩🟩🟩 | Bulk mail campaigns to your app's own users; audiences from a collection, a view or SQL. In the standard binary |
| [mailrelay](ext/mailrelay/README.md) | · | ✓ | ✓ | ✓ | 🟧🟧⬜ | Outgoing mail for instances without SMTP, through the operator's relay as `<instance>@<domain>`, with limits |
| [pages](ext/pages/README.md) | ✓ | ✓ | ✓ | ✓ | 🟥⬜⬜ | Server-rendered multi-page applications (PocketPages) |
| [storage](ext/storage/README.md) | ✓ | ✓ | ✓ | ✓ | 🟥⬜⬜ | A default storage backend when an app's own S3 is off |
| [s3redirect](ext/s3redirect/README.md) | ✓ | ✓ | ✓ | ✓ | 🟥⬜⬜ | File downloads as signed S3 redirects |
| [vacuum](ext/vacuum/README.md) | ✓ | ✓ | ✓ | ✓ | 🟧🟧⬜ | Scheduled SQLite VACUUM |
| [deploy](ext/deploy/README.md) | ✓ | ✓ | ✓ | ✓ | 🟧🟧⬜ | Deploy API: push hooks, migrations and public files |
| [schedules](ext/schedules/README.md) | · | ✓ | ✓ | ✓ | 🟧🟧⬜ | Scheduled webhooks that wake sleeping instances |

Columns are the [four ways to run it](#four-ways-to-run-it): **Core** is one app (level 1), **Pocker** many instances in one process (level 2), **Mothership** many edges and regions (level 3), **Multitenant** many owners (level 4). ✓ works there, · not available.

🟥⬜⬜ not started · 🟧🟧⬜ in progress (working code exists, not yet a plugin here) · 🟩🟩🟩 done

Plugins live in [`ext/`](ext), each as its own Go module. Higher levels pass settings down to the plugins below through
each instance's `plugins:` block (e.g. tenants sets an owner's trusted IPs for the firewall), so plugins don't call each
other.

## Contribute a plugin

Plugins are welcome. Read [Create your own plugin](docs/plugins.md), then open a pull request that adds
`ext/<your-plugin>/`. Write it against the plugin contract ([`ceplugin`](ceplugin/ceplugin.go)) and the same plugin works at
every level: on a single app and on every instance under pocker. Keep changes to PocketBase's own code to the minimum hook your plugin needs, and include a small
`example/main.go` that builds and shows the feature.

MIT, like PocketBase.

---

<p align="center">
    <a href="https://pocketbase.io" target="_blank" rel="noopener">
        <img src="https://i.imgur.com/aCBbjKx.png" alt="PocketBase - open source backend in 1 file" />
    </a>
</p>

<p align="center">
    <a href="https://pkg.go.dev/ganigeorgiev.com/pocketbase/pocketbase" target="_blank" rel="noopener"><img src="https://godoc.org/ganigeorgiev.com/pocketbase/pocketbase?status.svg" alt="Go package documentation" /></a>
</p>

[PocketBase](https://pocketbase.io) is an open source Go backend that includes:

- embedded database (_SQLite_) with **realtime subscriptions**
- built-in **files and users management**
- convenient **Superuser dashboard UI**
- and simple **REST-ish API**

**For documentation and examples, please visit https://pocketbase.io/docs .**

> **NB!** Please keep in mind that PocketBase is still under active development
> and full backward compatibility is not guaranteed before reaching v1.0.0.


## API SDK clients

The easiest way to interact with the PocketBase Web APIs is to use one of the official SDK clients:

- **JavaScript - [pocketbase/js-sdk](https://ganigeorgiev.com/pocketbase/js-sdk)** (_Browser, Node.js, React Native_)
- **Dart - [pocketbase/dart-sdk](https://ganigeorgiev.com/pocketbase/dart-sdk)** (_Web, Mobile, Desktop, CLI_)

You could also check the recommendations in https://pocketbase.io/docs/how-to-use/.


## Overview

### Use as standalone app

You could download the prebuilt executable for your platform from the [Releases page](https://ganigeorgiev.com/pocketbase/pocketbase/releases).
Once downloaded, extract the archive and run `./pocketbase serve` in the extracted directory.

The prebuilt executables are built from [`cmd/pocketbase`](cmd/pocketbase/main.go): PocketBase's `examples/base` plus the CE plugins that ship by default. They come with the JS VM plugin enabled, which lets you extend PocketBase with JavaScript (_see [Extend with JavaScript](https://pocketbase.io/docs/js-overview/)_), with [pages](ext/pages/README.md), which serves a `pb_pages` folder next to `pb_data` whenever it exists, and with [bulkmail](ext/bulkmail/README.md), which adds Mail to the dashboard for campaigns to the app's own users.

### Use as a Go framework/toolkit

PocketBase is distributed as a regular Go library package which allows you to build
your own custom app specific business logic and still have a single portable executable at the end.

Here is a minimal example:

0. [Install Go 1.27+](https://go.dev/doc/install) (_if you haven't already_)

1. Create a new project directory with the following `main.go` file inside it:
    ```go
    package main

    import (
        "log"

        "ganigeorgiev.com/pocketbase/pocketbase"
        "ganigeorgiev.com/pocketbase/pocketbase/core"
    )

    func main() {
        app := pocketbase.New()

        app.OnServe().BindFunc(func(se *core.ServeEvent) error {
            // registers new "GET /hello" route
            se.Router.GET("/hello", func(re *core.RequestEvent) error {
                return re.String(200, "Hello world!")
            })

            return se.Next()
        })

        if err := app.Start(); err != nil {
            log.Fatal(err)
        }
    }
    ```

2. To init the dependencies, run `go mod init myapp && go mod tidy`.

3. To start the application, run `go run main.go serve`.

4. To build a statically linked executable, you can run `CGO_ENABLED=0 go build` and then start the created executable with `./myapp serve`.

_For more details please refer to [Extend with Go](https://pocketbase.io/docs/go-overview/)._

### Building and running the repo main.go example

To build the standalone executable, like the prebuilt ones in the releases page, run `go build` inside the `cmd/pocketbase` directory (`examples/base` builds plain PocketBase without the CE plugins):

0. [Install Go 1.27+](https://go.dev/doc/install) (_if you haven't already_)
1. Clone/download the repo
2. Navigate to `cmd/pocketbase`
3. Run `CGO_ENABLED=0 go build` to build a binary for your current environment
   _(or to target other platforms use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build`; see https://go.dev/doc/install/source#environment)_
4. Start the created executable by running `./pocketbase serve`.

Note that the supported build targets by the pure Go SQLite driver at the moment are:

| GOOS    | GOARCH  |
|---------|---------|
| darwin  | amd64   |
| darwin  | arm64   |
| freebsd | 386     |
| freebsd | amd64   |
| freebsd | arm     |
| freebsd | arm64   |
| linux   | 386     |
| linux   | amd64   |
| linux   | arm     |
| linux   | arm64   |
| linux   | loong64 |
| linux   | ppc64le |
| linux   | riscv64 |
| linux   | s390x   |
| netbsd  | amd64   |
| openbsd | amd64   |
| openbsd | arm64   |
| windows | 386     |
| windows | amd64   |
| windows | arm64   |

### Testing

PocketBase comes with mixed bag of unit and integration tests.
To run them, use the standard `go test` command:

```sh
go test ./...
```

Check also the [Testing guide](http://pocketbase.io/docs/testing) to learn how to write your own custom application tests.


## Security

If you discover a security vulnerability within PocketBase, please submit a [private bug](https://ganigeorgiev.com/pocketbase/discussions/new/Bugs?private=1) OR send an e-mail to **support at pocketbase.io**.

You could find more details in the project [Security policy](https://ganigeorgiev.com/pocketbase/pocketbase/files/SECURITY.md).


## Contributing

PocketBase is free and open source project licensed under the [MIT License](https://ganigeorgiev.com/pocketbase/pocketbase/files/LICENSE.md).

You are free to do whatever you want with it, even offering it as a paid service
_(if you decide to do the latter, please make it clear that your service is not affiliated with the PocketBase project to avoid people wrongly submitting issues related to your service in the project's tracker)_.

You could help me by:

- [Report bugs and security issues](https://ganigeorgiev.com/pocketbase/discussions?category=Bugs)
- [Suggesting new features](https://ganigeorgiev.com/pocketbase/discussions?category=Feature+requests)
- [Help other people in the discussions](https://ganigeorgiev.com/pocketbase/discussions)
