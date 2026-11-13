# firewall

Peer allow-lists, trusted proxies and client IPs, and per-IP and per-instance rate limits for a multi-instance host
([ext/host](../host/README.md)), with or without the fleet layer.

> **Status.** Working: Go module `github.com/pockethost/pocketone/ext/firewall`. Linking it registers it with the
> host; it turns on when the config has `firewall:` or `rate_limits:` (the `pocker` command always turns it on).

```yaml
firewall:
  allow_cidrs: []                 # peers allowed to connect (empty = everyone)
  trusted_proxies: []             # peers whose client-IP headers are believed
  client_ip_headers: [CF-Connecting-IP, True-Client-IP, X-Forwarded-For, X-Real-IP]
  trusted_clients: []             # client IPs that get the trusted rate limits on every instance
  rate_limit_header_prefix: X-RateLimit
rate_limits: { ip_hourly: 1000 }  # defaults; per instance under plugins.firewall.rate_limits
```

A single app doesn't need it today (stock PocketBase has per-IP rate limits and trusted proxies); a single-app
adapter may come later for the peer allow-list and budgets.

## License

MIT
