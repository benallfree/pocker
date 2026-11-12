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

In dev mode (`serve --dev`), an app without SMTP doesn't use the host's `sendmail`: its mail is caught in-process and
you read it at http://127.0.0.1:8025.

## Core Extensions

These are extensions to the PocketBase core that ship with every binary. They do not add runtime overhead if you
choose not to use them.

| Plugin | Description | Status |
|---|---|---|
| **[PITR](ext/pitr/README.md)** (Litestream) | Point-in-time restore of your database, from Settings → Backups or `pocketbase pitr restore`. On by default; `--pitr=false` turns it off. | Stable |
| **[Auto TLS](ext/autotls/README.md)** | On-demand certificates and wildcards through ACME. | Stable under Pocker; single app: Planned |
| **[Bulk Mail](ext/bulkmail/README.md)** | Campaigns to your app's own users, with audiences from a collection, a view or SQL. | Beta |
| **[Pages](ext/pages/README.md)** (formerly PocketPages) | Server-rendered pages from a `pb_pages` folder. | Beta |
| **[Vacuum](ext/vacuum/README.md)** | Scheduled SQLite VACUUM. | Planned |
| **[MCP](ext/mcp/README.md)** | An MCP endpoint at `/mcp` so agents can manage your app and call its tools. | Beta |
| **[OAuth Server](ext/oauth-server/README.md)** | Your app as an OAuth provider; `pocketbase login` and MCP sign-in use it. | Beta |
| **[S3 Redirect](ext/s3redirect/README.md)** | File downloads as short-lived signed S3 redirects. | Beta |
| **[Tokens](ext/tokens/README.md)** | Long-lived API tokens (`pbt_…`) with scopes. | Beta |
| **[Deploy](ext/deploy/README.md)** | `pocketbase deploy \| dev \| pull \| logs` and the deploy API. | Beta |
| **[Remotes](ext/remotes/README.md)** | Named remotes and saved logins for the `pocketbase` command. | Beta |

## Community Plugins

These plugins are maintained by PocketHost and the community and are MIT. You can mix and match them to create your
own custom PocketBase experience.

**Run many instances**

| Plugin | Description | Status |
|---|---|---|
| **[Pocker](ext/pocker/README.md)** | Hundreds of PocketBase instances in one Go process, routed by hostname. | Stable |
| **[Schedules](ext/schedules/README.md)** | Scheduled webhooks that wake sleeping instances (headed into Pocker). | Planned |
| **[Mothership](ext/mothership/README.md)** | A control plane and its edges, across regions. Needs Pocker. | Planned |
| **[Sandbox](ext/sandbox/README.md)** | Confines untrusted JS hooks, SQL and outbound network ([how is this safe?](ext/sandbox/README.md#how-is-this-safe)). Required for Multitenant. | Stable |
| **[Firewall](ext/firewall/README.md)** | Peer allow-lists, trusted proxies and rate limits. | Stable |

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
| **[Dev Users](ext/devusers/devusers.go)** | Local testing only: a superuser and a user with password `password`. Every plugin's `example/` uses it. | Stable |

**Stable** runs in PocketHost production · **Beta** complete and tested, not yet in production · **Alpha** working
code, still changing · **Planned** a plan and a README so far.

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

The prebuilt executables are built from [`cmd/pocketbase`](cmd/pocketbase/main.go): PocketBase's `examples/base` plus the CE plugins that ship by default. They come with the JS VM plugin enabled, which lets you extend PocketBase with JavaScript (_see [Extend with JavaScript](https://pocketbase.io/docs/js-overview/)_), and with the [Core Extensions](#core-extensions) above.

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
