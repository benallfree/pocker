# sandbox

The JS sandbox for untrusted instances: file access confined to the instance folder, host primitives removed, CPU and memory caps, SQLite authorizer, public-only outbound network.

**Runs on:** Any app; required by pocker for untrusted tenants.

> **Status.** Working: Go module `github.com/pockerbase/pocketbase/ext/sandbox`. It plugs into PocketBase's JS plugin
> through two hooks, `jsvm.Config{OnInit: sb.OnInit, OnCall: sb.OnCall}`; [`example/main.go`](example/main.go) runs
> one sandboxed app. [pocker](../pocker/README.md) uses it for every instance; its attack tests live there. The SQL
> guard, outbound network rules and memory watch are still in pocker and move here next.

## License

MIT
