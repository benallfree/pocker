# sandbox

The JS sandbox for untrusted instances: file access confined to the instance folder, host primitives removed, CPU and memory caps, SQLite authorizer, public-only outbound network.

**Runs on:** Any app; required by pocker for untrusted tenants.

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/sandbox`. It plugs into PocketBase's JS plugin
> as a jsvm extension, `jsvm.Config{Extensions: []jsvm.Extension{sb}}` (its `OnInit`, `OnCall` and `HTTPClient`); [`example/main.go`](example/main.go) runs
> one sandboxed app. [pocker](../pocker/README.md) uses it for every instance; its attack tests live there. The SQL
> guard, outbound network rules and memory watch are still in pocker and move here next.

## How is this safe?

With [pocker](../pocker/README.md), PocketBase CE can serve hundreds of PocketBase instances from one Go process, each with its own owner, hooks and data.
This section explains why that's safe enough to run code from people you don't know, and where it isn't.

Configuration and the engine API are in [POCKER.md](../pocker/POCKER.md). The attack tests are in [`sandbox_test.go`](../pocker/sandbox_test.go).

### Why one process

Most hosted PocketBase instances are idle most of the time. A container per instance means paying idle memory and a
cold start of a second or more for each one. In one process an instance's app starts on its first request in tens of
milliseconds, costs a few MB while it runs, and shares the Go runtime with everyone else.

What you give up is the kernel boundary between tenants. Everything below replaces it with checks inside the process.

### The threat

Assume every instance's owner is hostile. They control:
- their `pb_hooks` (JavaScript, run by PocketBase's goja VM);
- their `pb_migrations`;
- their collections, rules and settings;
- any SQL their hooks run;
- requests to their own instance.

They must not be able to:
- read or change another instance's files, database, environment or settings;
- read the host's files, environment, credentials or private network;
- take the process down, or starve everyone else of CPU and memory.

### The layers

#### 1. Each instance is its own PocketBase app

- Every instance has its own `core.App`, database, settings, JS VMs, migrations list, settings encryption key and
  log.
- Requests are routed by hostname to exactly one app. Nothing in one app holds a reference to another.
- In production only hostnames the instance source maps are served; unknown hosts get 404 and never create a folder.

#### 2. Files: confined by the kernel, not by string checks

Every file API reachable from JS goes through an `os.Root` opened on the instance's folder. That covers:
- `$os.*`
- `$filepath.glob`/`walk`
- `$filesystem.fileFromPath`
- `$template.loadFiles`
- `$apis.static`
- `require()`

The kernel resolves each path beneath that root and refuses `..` and symlinks that lead out. There is no path
string for an attacker to get clever with.

#### 3. Host primitives: removed

The following are gone from JS:
- process control: `$os.cmd`, `$os.exec`, `$os.exit`, `$os.args`;
- `$filesystem.local`;
- PocketBase internals that reach the whole process: `$app.restart`, `bootstrap`, `onServe`, `onTerminate`, raw
  `hook` triggers, `*sql.DB` handles;
- Go's `os/exec`, `net`, `syscall` and `database/sql/driver`.

A field-name mapper hides them, so they read as `undefined`. Everything a normal app uses still works: `routerAdd`,
`onRecord*`, `cronAdd`, `$app.findRecordById` and the rest.

#### 4. Environment: only the instance's own

`$os.getenv` and `process.env` return the instance's own secrets and nothing else. Platform values never go through
the environment:
- the settings encryption key;
- superusers the host keeps in sync;
- private tokens for the host's Go code.

#### 5. SQL: SQLite's own authorizer

Raw SQL is allowed (`$app.db().newQuery(...)`), because PocketBase apps depend on it. Instead of parsing statements,
PocketBase CE installs SQLite's authorizer callback on every connection, so SQLite itself refuses:
- `ATTACH` and `VACUUM INTO` outside the instance's `pb_data`;
- `load_extension`;
- the pragmas that move SQLite's files elsewhere.

Statements without a deadline get one (`max_query_duration`).

#### 6. Outbound network: public addresses only

PocketBase CE installs one dialer process-wide, on Go's default HTTP transport and PocketBase's SMTP client. It covers:
- `$http.send`
- `$filesystem.fileFromURL`
- S3 storage and backups
- OAuth2/JWKS fetches
- SMTP

The dialer refuses:
- loopback, private ranges, link-local and cloud metadata;
- CGNAT, multicast, reserved and documentation ranges;
- the IPv6 forms of all of these, including IPv4-in-IPv6.

It checks the address actually connected to, after DNS, so DNS rebinding doesn't help. `$http.send` timeouts and
response sizes are capped.

#### 7. CPU, memory and concurrency

| Resource | What's enforced |
|---|---|
| CPU | Every JS call (hook, route, cron, migration) is interrupted after `max_js_execution`. `sleep()` is capped. |
| Concurrency | At most `max_concurrent_js` JS calls per instance at once; more wait, then fail. Per-IP and per-instance rate and concurrency limits sit in front of every request. |
| One-shot memory | Built-ins that allocate a big result in one step refuse results over `max_js_allocation`: `repeat`, `padStart`, `join`, typed arrays, `ArrayBuffer`, `Buffer`, file reads and others. Recursion depth is capped, so a JS stack overflow can't crash Go. |
| Gradual memory | goja can't account memory per VM, so a watcher samples the process heap every 50 ms. Above the soft limit it interrupts long JS calls and gives their instance a strike. Repeated strikes pause the instance (503 with `Retry-After`), with backoff. |

Every limit has a process default and a per-instance override.

#### 8. Failures stay with their instance

- A broken hooks file, a panic during start or a failed migration fails that instance alone: it answers 502 and logs
  to its own `logs/exec.log`. Every other instance keeps serving.
- `console.*` output goes to the instance's own log, never to the host's.

### What it doesn't cover

Be clear about these before you host strangers:

- **Memory attribution is best-effort.** The watcher blames whoever is running JS when the heap grows. That's usually
  right, but not guaranteed. A hook that allocates faster than the 50 ms sample, through a path the one-shot caps
  don't cover, can still push the process over its limit.
- **It's one address space.** A bug in Go, goja, SQLite or PocketBase CE's own checks would cross tenants in a way a
  container boundary wouldn't. The sandbox tests cover the known paths, but they don't prove nothing else exists.
- **No OS-level layer yet.** Seccomp, Landlock and a second, OS-level egress filter aren't built in.

### How to run it

For untrusted tenants, add the OS layers yourself:
- run PocketBase CE as a non-root user that owns only the instances folder;
- set `memory_limit` and run it under a cgroup memory limit, so the worst case is a restart, not a host OOM;
- add an OS firewall rule that lets that user reach public addresses only, an egress layer independent of the
  process;
- keep secrets for host code out of the process environment; use `private:` values instead;
- put a proxy or the built-in firewall in front, with rate limits on.

If you only run your own apps, the defaults are already more than stock PocketBase gives you.

## License

MIT
