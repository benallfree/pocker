# host

PocketOne's multi-instance host: many PocketBase instances behind one listener, routed by hostname, run by exactly one
runtime provider ([pocker](../pocker/README.md) in-process, or docker in containers).

> **Status.** Working: Go module `github.com/pockethost/pocketone/ext/host`. pocker runs on it (`pocker.Server` is a
> thin layer over `host.Host`). Next: the stock `pocketone` binary hands `serve` to it when `pocketbase.yaml` names a
> provider.

## Pieces

| Piece | What it does |
|---|---|
| `Instance`, `InstanceSource`, `YAMLSource` | The instance model and where instances come from (`instances.yaml`, a control plane). |
| `Directory` | Instances by hostname and id, kept current from the source. |
| `Handler` | Health path → peer check → hostname → blocked → **remote** (the `Forward` hook) or **local** (the runtime) → gates → serve. |
| `Runtime` | What a provider implements (+ optional `Prechecker`, `Syncer`, `Statser`, `Shutdowner`). |
| `Gate`, `RegisterGate` | Request gates; a linked gate (e.g. [ext/firewall](../firewall/README.md)) turns on when the config has its section. |
| `Serve`, `TLSProvider` | Listeners: plain, TLS (a provider such as autotls, or cert files reloaded on change), redirect, graceful shutdown. |
| `Host` | All of the above assembled: source, directory, gates, wrappers, forward hook, listeners, background work, one runtime. |

The fleet (forwarding to other edges, edge lists, a mothership) is [ext/edge](../edge/README.md), on top; it fills
`Forward`.

## License

MIT
