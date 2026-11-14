# pocker

Many PocketBase instances in one process (like Docker, but for PocketBase), routed by hostname, each started on its first request. Instances come from a YAML file or any provider.

**Runs on:** it is the multi-instance host; [mothership](../mothership/README.md) and other host-level plugins
register on it.

> **Status.** Working: this folder is the Go module `github.com/pockethost/pocketone/ext/pocker` (runnable
> [`example/`](example), configuration and API in [POCKER.md](POCKER.md)). Pocker is a runtime provider on
> [ext/host](../host/README.md) (routing, listeners), run by the stock `pocketone` binary with `provider: pocker`; the
> firewall is [ext/firewall](../firewall/README.md) and forwarding to other edges [ext/edge](../edge/README.md); auto TLS
> and Server-Timing are plugins of their own ([autotls](../autotls/README.md), [servertiming](../servertiming/README.md)).

## As a runtime provider

`provider: pocker` in pocketone.yaml: `pocker.Provider` reads the `pocker` section (`root`,
`egress_allow`, `hooks_pool_size`, instance limit defaults) and runs every instance of an [ext/host](../host/README.md)
in-process. Instance versions are ignored: every instance runs the PocketOne code in the binary. Plugins read their own
sections (`autotls`, `pitr`, `storage`, `s3redirect`, `mailrelay`).

## Untrusted instances

Pocker runs every instance's JS hooks in the [sandbox](../sandbox/README.md). For why that's safe enough to host code
from people you don't know, and where it isn't, see [How is this safe?](../sandbox/README.md#how-is-this-safe).

## License

MIT
