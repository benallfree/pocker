# autotls

Automatic certificates through ACME, with CertMagic (Caddy's certificate engine): on demand for every served
hostname, wildcards through DNS-01, more CAs.

**Runs on:** Single app (in the standard `pocketone` binary), or many under [pocker](../pocker/README.md).

> **Status.** Stable under pocker; Beta on a single app. Go module `github.com/pockethost/pocketone/ext/autotls`.

## Single app

PocketBase already gets Let's Encrypt certificates for `pocketbase serve example.com`. Add `--autotls` and
CertMagic serves them instead, which adds other CAs, extra domains and wildcards:

```sh
pocketbase serve example.com --autotls --autotlsEmail ops@example.com
pocketbase serve example.com --autotls --autotlsDomains "*.example.com" --autotlsDNS cloudflare   # token in $CF_API_TOKEN
```

| Flag | Meaning |
|---|---|
| `--autotls` | Turn it on. Off by default, and idle unless the app serves HTTPS (`serve <domain>` or `--https`). |
| `--autotlsEmail` | ACME account email. |
| `--autotlsCA` | `letsencrypt` (default), `letsencrypt-staging`, `zerossl`, or an ACME directory URL. |
| `--autotlsDomains` | Extra domains, obtained at start. Wildcards need `--autotlsDNS`. |
| `--autotlsDNS` | DNS provider for DNS-01 (`cloudflare`). |
| `--autotlsDNSTokenEnv` | Environment variable with the provider's API token (default `CF_API_TOKEN`). |

Certificates are issued for the domains given to `serve` (and their `www.` variants) plus `--autotlsDomains`; any
other hostname is refused at the handshake. Challenges are TLS-ALPN-01 on the HTTPS port, or DNS-01 with a provider.
HTTP-01 isn't used, because port 80 is PocketBase's redirect server. Certificates live in `pb_data/.autotls`.

In Go, register it like any CE plugin:

```go
ceplugin.MustRegister(app, autotls.NewApp(autotls.Config{Auto: true, Email: "ops@example.com"}))
```

## Under pocker

`autotls.New()` is a pocker server plugin, configured by the `tls:` block of `pocker.yaml`
(`tls: {auto, email, ca, storage, domains, dns}`); it's bundled in the `pocker` command. Certificate files
(`tls.cert`/`tls.key`) stay a core feature.

## License

MIT
