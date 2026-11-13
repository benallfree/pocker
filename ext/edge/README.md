# edge

The fleet layer for a multi-instance host ([ext/host](../host/README.md)): requests for instances another edge owns
are forwarded there over the private network. Works with either runtime provider.

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/edge`. The host core moved to ext/host and the
> firewall to [ext/firewall](../firewall/README.md) (2026-11-13); ext/edge still re-exports the host names for one
> release.

## Pieces

| Piece | What it does |
|---|---|
| `Router` | Forwards a request for an instance another edge owns: holds and retries while that edge is healthy, fails fast once it is down, never bounces a forwarded request, CORS at the entry edge. Plug `Router.Forward` into `host.Host.Forward`. |
| Edge providers | Where the other edges are (`EdgeSource`). Built in: `YAMLProvider` (`edges.list`); combine several with `MultiProvider`. [ext/mothership](../mothership/README.md)'s mirror is one from a mothership. |

## Config

```yaml
edges:
  self: lax-1
  list:                           # fixed entries (with a mothership too, they win for the same edge)
    - { id: sgp-1, internal_url: "http://10.88.0.4:8080", region: sgp }
```

An instance owned by another edge has `remote: edge:@sgp-1`. Without a mothership, instances come from
`instances.yaml`; with one (a `mothership:` section, see [ext/mothership](../mothership/README.md)), instances and
edges both come from it.

## License

MIT
