# oauth-server

Makes a PocketBase app an OAuth 2.1 authorization server whose access tokens are [API tokens](../tokens/README.md).
`pocketbase login` signs in to it in the browser (or with a code on another device), and MCP clients such as Claude
or ChatGPT connectors can register themselves and connect.

**Runs on:** Single app. Requires [tokens](../tokens/README.md).

```go
tokens.MustRegister(app, tokens.Config{})
remotes.MustRegister(app, remotes.Config{}) // optional: advertises "oauth" in /_pocker/whoami so login finds it
oauthserver.MustRegister(app, oauthserver.Config{
    Collections:         []string{"_superusers"}, // who may sign in (default); their records must be able to own tokens
    AccessTTL:           30 * 24 * time.Hour,     // default
    RefreshTTL:          180 * 24 * time.Hour,    // default, renewed on each refresh
    DynamicRegistration: false,                   // POST /oauth/register for MCP clients
    Branding:            oauthserver.Branding{LogoURL: "/logo.svg", Accent: "#4f46e5", Footer: "Acme Inc."},
})
```

Try it: `go run ./example serve`, then `go run ./example login http://127.0.0.1:8090` (logins from
[devusers](../devusers/devusers.go): admin@example.com / password).

## Endpoints

| Path | What |
|---|---|
| `GET /.well-known/oauth-authorization-server` | Metadata (RFC 8414). The issuer is `Config.Issuer`, else the request's own URL. |
| `GET /.well-known/oauth-protected-resource` | Resource metadata (RFC 9728), for MCP clients. |
| `GET/POST /oauth/authorize` | Sign-in and consent pages. Authorization code with PKCE (S256 only; required). |
| `POST /oauth/token` | `authorization_code`, `refresh_token` (rotated; the old access token is revoked), `urn:ietf:params:oauth:grant-type:device_code`. |
| `POST /oauth/device` + `/oauth/device/verify` | Device flow (RFC 8628): codes like `WDJB-MJHT`, valid 10 minutes. |
| `POST /oauth/revoke` | Revocation (RFC 7009): an access token or a refresh token (revokes both). |
| `POST /oauth/register` | Dynamic client registration (RFC 7591), when `DynamicRegistration` is on. Public clients by default. |

Access tokens are `pbt_…` API tokens with the client's id and the granted scopes, so they show up (and can be
revoked) under Settings → API tokens, and every scope a plugin registers can be requested. No scope requested means
the account's full rights; the consent page says so.

## Sign-in

The pages sign in with what the collection allows: password, an emailed one-time code (OTP), and both when MFA is on.
Failed attempts are limited per IP (10 per 5 minutes). The session is a 15-minute HttpOnly cookie on `/oauth`; forms
carry a CSRF token. Sign-in with OAuth2 providers (Google, GitHub) isn't offered yet.

## Clients

- **`pocketbase-cli`** is built in: public, any `http://127.0.0.1:<port>/…` or `[::1]` redirect, device codes. It's
  what `pocketbase login` uses ([remotes](../remotes/README.md)).
- Registered clients live in `_oauth_clients`. Loopback redirect URIs match any port (RFC 8252); other URIs match
  exactly; plain `http` only for loopback.
- CLI: `pocketbase oauth clients list`, `oauth clients add --name "My app" --redirect https://…/callback [--public]`
  (prints the client id, and the secret once for a confidential client), `oauth clients rm <client_id>`.

## Branding and custom pages

The built-in pages (sign-in, one-time code, consent, device code, done, error) use Settings → Application name, plus
`Branding` (logo, accent color, footer). They follow the system's light or dark mode and work on phones.

To replace them:
- `Config.Render(e, page, data)` renders any page itself (return `handled=false` to fall back), with `PageData`: the
  page name, branding, the form `Action`, `Hidden` fields and `CSRF` token every form must post back, the client, the
  scopes, the account, errors.
- With [pages](../pages/README.md): when the pages service offers `RenderTemplate(e, name, data map[string]any)
  (handled bool, err error)`, the server asks it for `oauth/<page>` first, so a project can theme the pages in
  PocketPages. The data keys are `PageData`'s in camelCase (`action`, `hidden`, `csrf`, `clientName`, `scopes`, …).

## License

MIT
