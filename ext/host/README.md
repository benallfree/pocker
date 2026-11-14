# host

PocketOne's multi-instance host: many PocketBase instances behind one listener, routed by hostname, run by exactly one
runtime provider ([pocker](../pocker/README.md) in-process, or docker in containers).

> **Status.** Working: Go module `github.com/pockethost/pocketone/ext/host`. pocker runs on it (`pocker.Server` is a
> thin layer over `host.Host`). Next: the stock `pocketone` binary hands `serve` to it when `pocketone.yaml` names a
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

## From pocketone.yaml

`host.FromConfig` builds a host from [ext/config](../config/README.md) sections: the provider named in `provider`
(registered with `RegisterProvider`: pocker, docker), listeners from `server` (`https` main, `http` redirects to it,
`plain` extra plain-HTTP addresses, `tls` cert files), instances from `instances` (`file`, `dev`) unless the binary
brings its own source, and every linked gate whose section is present (e.g. ext/firewall's `firewall` /
`rate_limits`). The stock `pocketone` binary does this for `serve` when `provider:` is set.

```yaml
server: { https: ":443", http: ":80", plain: [":8080"], tls: { cert: cert.pem, key: key.pem } }
provider: docker          # or pocker
docker: { image: alpine:3.20, binaries: ./bin, manifest: ./versions.json, root: ./instances }
instances: { file: ./instances.yaml }
firewall: { allow_cidrs: [10.0.0.0/8] }
```

Switching provider is a config change: both use `<root>/<id>`. Stop the containers first when leaving docker (they
keep running across a host restart on purpose), and remember data only moves forward: an instance started on a newer
PocketBase than wrote its data is upgraded.

## License

MIT
