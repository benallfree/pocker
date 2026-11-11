# pocker

The multitenant engine (like Docker, but for PocketBase): many PocketBase apps in one process, routed by hostname, each started on its first request. Instances come from a YAML file or any provider.

**Runs on:** it is the multitenant host; the other host-level plugins register on it.

> **Planned.** Nothing is published here yet. This folder will hold the plugin's Go module
> (`github.com/pockerbase/pocketbase/ext/pocker`), its tests and a minimal `example/main.go` that builds and shows the
> feature. See [Create your own plugin](../../docs/plugins.md).

## License

MIT
