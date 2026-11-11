# firewall

Peer allow-lists, trusted proxies and per-IP rate limits; under pocker, per-instance and cross-instance limits too.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/firewall`, a plugin for [pocker](../pocker/README.md)
> (`pocker.Server{Plugins: []pocker.Plugin{firewall.New()}}`), bundled in the `pocker` command.
> Settings: `firewall:` and `rate_limits:` in pocker.yaml; per instance `plugins.firewall: {rate_limits, trusted_ips}`.

## License

MIT
