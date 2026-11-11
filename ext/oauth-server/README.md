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
| `GET/POST /oauth/authorize` | Sign-in and consent pages. Authorization code with PKCE (S256 only; required). |
| `POST /oauth/token` | `authorization_code`, `refresh_token` (rotated; the old access token is revoked), `urn:ietf:params:oauth:grant-type:device_code`. |
| `POST /oauth/device` + `/oauth/device/verify` | Device flow (RFC 8628): codes like `WDJB-MJHT`, valid 10 minutes. |
| `POST /oauth/revoke` | Revocation (RFC 7009): an access token or a refresh token (revokes both). |
| `POST /oauth/register` | Dynamic client registration (RFC 7591), when `DynamicRegistration` is on or a plugin called `EnableDynamicRegistration()` (ext/mcp does). Public clients by default. |

Protected resource metadata (RFC 9728) belongs to the resource: ext/mcp serves `/.well-known/oauth-protected-resource`.

**Audience (RFC 8707).** A `resource` parameter on `/oauth/authorize`, `/oauth/device` or `/oauth/token` (a URL on this
server) binds the access token to it: the token then only authenticates under that URL's path, so a token issued for
`/mcp` can't deploy and a deploy token can't drive MCP. Refreshed tokens keep it. `offline_access` is accepted in
`scope` (refresh tokens are always issued) and listed in `scopes_supported`.

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
- **Client ID metadata documents (CIMD):** an `https://` client id is fetched as a JSON document (`client_id` must
  equal the URL; its `client_name` and `redirect_uris` are used; cached 10 minutes). Only `Config.CIMDHosts` are
  trusted (default `claude.ai`, `claude.com`, so Claude and Claude Code connect without registering); the metadata
  says `client_id_metadata_document_supported`.
- Registered clients live in `_oauth_clients`. Loopback redirect URIs match any port (RFC 8252); other URIs match
  exactly; plain `http` only for loopback.
- CLI: `pocketbase oauth clients list`, `oauth clients add --name "My app" --redirect https://…/callback [--public]`
  (prints the client id, and the secret once for a confidential client), `oauth clients rm <client_id>`.

## Branding and custom pages

The built-in pages (sign-in, one-time code, consent, device code, done, error) use Settings → Application name, plus
`Branding` (logo, accent color, footer). They follow the system's light or dark mode and work on phones.

To replace them:
- **Eject (with [pages](../pages/README.md)):** `pocketbase oauth eject` writes the pages as PocketPages templates to
  `pb_pages/_private/oauth/` (`signin.ejs`, `otp.ejs`, `consent.ejs`, `device.ejs`, `done.ejs`, `error.ejs`, and the
  shared `head.ejs` / `foot.ejs` / `hidden.ejs` with the CSS). It prints each file, refuses to overwrite existing ones
  unless `--force`, and from then on the server renders those templates (missing ones fall back to the built-in
  page; delete them to go back). `_private` keeps them from being served as routes. The data is `ctx.data`
  (`data.appName`, `data.action`, `data.hidden`, `data.csrf`, `data.clientName`, `data.scopes`, `data.error`, …,
  `PageData` in camelCase); every POST form must include `include('/oauth/hidden')` and a `step` button.
- **`Config.Render(e, page, data)`** renders any page itself (return `handled=false` to fall back), with
  `PageData`. It wins over ejected templates.

## License

MIT
