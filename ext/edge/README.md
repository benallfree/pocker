# edge

The front door of a server that runs PocketBase instances: the firewall, then dispatch. Each request is checked
(allowed peers, client IP, rate limits), matched to an instance by hostname, and either forwarded to the edge that
owns that instance or served by a local runtime.

**Runs on:** any host of many instances: [pocker](../pocker/README.md) today; docker and standalone runtimes plug in
the same way.

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/edge`. pocker builds on it and the `pocker`
> command turns the firewall on.

## Pieces

| Piece | What it does |
|---|---|
| `Directory` | Instances by hostname and id, kept current from an `InstanceSource` (`instances.yaml`, or a control plane). |
| `Firewall` | A `Gate`: `allow_cidrs`, `trusted_proxies` + `client_ip_headers`, per-IP and per-instance rate limits (`rate_limits:`, per instance `plugins.firewall`). |
| `Handler` | Health path → peer check → hostname → blocked → **remote** (forward) or **local** (pick a `Runtime`) → admit → serve. |
| `Runtime` | Where a local instance runs: `pocker` (in-process), `docker`, `standalone` (one CE process). An instance can name one (`runtime:`); otherwise the first that serves it. |
| `Router` | Forwards a request for an instance another edge owns over the private network: holds and retries while that edge is healthy, fails fast once it is down, never bounces a forwarded request. |
| Edge providers | Where the other edges are. Built in: `YAMLProvider` (`edges.list`) and `MothershipProvider` (a mothership's `edges` collection, read with a superuser login from local config), combined with `MultiProvider`. Bring your own by implementing `EdgeSource`. |
| `Serve` | Listeners: plain, TLS (a `TLSProvider` such as autotls, or a cert/key pair reloaded on change), HTTP → HTTPS redirect, graceful shutdown. |

## Config

```yaml
firewall:
  allow_cidrs: []                 # peers allowed to connect (empty = everyone)
  trusted_proxies: []             # peers whose client-IP headers are believed
  client_ip_headers: [CF-Connecting-IP, True-Client-IP, X-Forwarded-For, X-Real-IP]
  trusted_clients: []             # client IPs that get the trusted rate limits on every instance
  rate_limit_header_prefix: X-RateLimit
rate_limits: { ip_hourly: 1000 }  # defaults; per instance under plugins.firewall.rate_limits

edges:
  self: lax-1
  list:                           # fixed entries (win over the mothership's for the same edge)
    - { id: sgp-1, internal_url: "http://10.88.0.4:8080", region: sgp }
  mothership:                     # or read the list from a mothership; $VARS expand from the environment
    url: http://10.88.0.10:8090
    email: $MOTHERSHIP_EMAIL
    password: $MOTHERSHIP_PASSWORD
    refresh: 30s
```

An instance owned by another edge has `remote: edge:@sgp-1` (or `edge:<mothership record id>`).

## License

MIT
