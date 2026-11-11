# mothership

A mothership and its edges: the control plane (instances, edges, regions, admin screens) and the edge side that follows it, each edge owning its own instances.

One operator (level 3): owners and accounts are the [tenants](../tenants/README.md) plugin (level 4), which extends
the mothership through its schema and feed hooks.

**Runs on:** Requires [pocker](../pocker/README.md).

> **Planned.** Nothing is published here yet. This folder will hold the plugin's Go module
> (`github.com/pockethost/pocketbase/ext/mothership`), its tests and a minimal `example/main.go` that builds and shows the
> feature. See [Create your own plugin](../../docs/plugins.md).

## License

MIT
