# pocker

Many PocketBase instances in one process (like Docker, but for PocketBase), routed by hostname, each started on its first request. Instances come from a YAML file or any provider.

**Runs on:** it is the multi-instance host; [mothership](../mothership/README.md) and other host-level plugins
register on it.

> **Status.** Working: this folder is the Go module `github.com/pockethost/pocketbase/ext/pocker` (CLI in
> [`cmd/pocker`](cmd/pocker), runnable [`example/`](example), configuration and API in [POCKER.md](POCKER.md)).  Routing,
> the firewall and forwarding to other edges come from [ext/edge](../edge/README.md); auto TLS and Server-Timing are
> plugins of their own ([autotls](../autotls/README.md), [servertiming](../servertiming/README.md)); the `pocker`
> command bundles them.

## Untrusted instances

Pocker runs every instance's JS hooks in the [sandbox](../sandbox/README.md). For why that's safe enough to host code
from people you don't know, and where it isn't, see [How is this safe?](../sandbox/README.md#how-is-this-safe).

## License

MIT
