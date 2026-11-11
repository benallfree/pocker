# autotls

Automatic certificates through ACME: on demand for every served hostname, wildcards through DNS-01, more CAs.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/autotls`, a plugin for [pocker](../pocker/README.md)
> (`pocker.Server{Plugins: []pocker.Plugin{autotls.New()}}`), bundled in the `pocker` command.
> Settings: `tls: {auto, email, ca, storage, domains, dns}` in pocker.yaml (certificate files stay core).

## License

MIT
