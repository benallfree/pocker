# pocker

Many PocketBase instances in one process (like Docker, but for PocketBase), routed by hostname, each started on its first request. Instances come from a YAML file or any provider.

**Runs on:** it is the multi-instance host; [mothership](../mothership/README.md) and other host-level plugins
register on it.

> **Status.** Working: this folder is the Go module `github.com/pockethost/pocketone/ext/pocker` (CLI in
> [`cmd/pocker`](cmd/pocker), runnable [`example/`](example), configuration and API in [POCKER.md](POCKER.md)).  Routing,
> pocker is a runtime provider on [ext/host](../host/README.md) (routing, listeners); the firewall is
> [ext/firewall](../firewall/README.md) and forwarding to other edges [ext/edge](../edge/README.md); auto TLS and Server-Timing are
> plugins of their own ([autotls](../autotls/README.md), [servertiming](../servertiming/README.md)); the `pocker`
> command bundles them.

## As a runtime provider

`provider: pocker` in pocketbase.yaml: `pocker.Provider` reads the `pocker` section (`root`, `memory_limit`,
`egress_allow`, `hooks_pool_size`, instance limit defaults) and runs every instance of an [ext/host](../host/README.md)
in-process. Instance versions are ignored: every instance runs the PocketOne code in the binary. Plugins configured
from a pocker.yaml document don't read pocketbase.yaml sections yet (they run with their defaults there).

## Untrusted instances

Pocker runs every instance's JS hooks in the [sandbox](../sandbox/README.md). For why that's safe enough to host code
from people you don't know, and where it isn't, see [How is this safe?](../sandbox/README.md#how-is-this-safe).

## License

MIT
