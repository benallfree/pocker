# tokens

API tokens for PocketBase: long-lived bearer secrets (`pbt_…`) owned by an auth record, optionally limited to scopes,
listed and revoked one by one. Send one as `Authorization: Bearer pbt_…` and the request runs as the token's owner.
JWTs keep working unchanged.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

```go
app := pocketbase.New()
tk := tokens.MustRegister(app, tokens.Config{
    Collections: []string{"_superusers", "users"}, // who may own tokens (default: _superusers)
    MaxTTL:      0,                                // cap on a token's lifetime (0: none)
})
```

Try it: `go run ./example serve` (logins from [devusers](../devusers/devusers.go): admin@example.com / password).

## What it adds

- **`_tokens` collection:** owner (collection id + record id), name, `hash` (sha256 of the secret; the secret is
  never stored), display prefix, scopes, expiry, last use (time + IP), OAuth client id. Owners can list, view and
  delete their own tokens through the records API.
- **Bearer auth:** a middleware just before PocketBase's JWT middleware. Invalid or expired tokens leave the request
  unauthenticated, like an invalid JWT. A token can't be exchanged for a JWT (`auth-refresh` and `impersonate`
  refuse it), so revoking a token really ends its access.
- **Routes:** `GET /api/tokens` (yours; superusers see all, `?ownerCollection=&owner=` filters), `POST /api/tokens`
  (`{name, scopes, ttl | expires}`; superusers may add `ownerCollection` + `owner`), `DELETE /api/tokens/{id}`,
  `POST /api/tokens/revoke-all`, `GET /api/tokens/scopes`. The secret is returned once, on create.
- **Dashboard:** Settings → API tokens (create, see last use, revoke).
- **CLI** (on a `*pocketbase.PocketBase`): `token create --superuser admin@example.com --name ci --scope deploy
  [--ttl 720h]` prints the secret; `token list`, `token revoke <id>… | --all --superuser <email>`, `token scopes`.
- A daily cron deletes expired tokens; deleting an owner deletes its tokens.

## Scopes

A token without scopes has its owner's full rights. Plugins register scopes; a scoped token authenticates only on its
scopes' paths (elsewhere: 403), plus `/_pocker/whoami`:

```go
tk.RegisterScope(tokens.Scope{Name: "deploy", Description: "Deploy code", Paths: []string{"/_pocker/deploy/"}})
se.Router.POST("/_pocker/deploy/v1/commit", h).Bind(apis.RequireSuperuserAuth(), tokens.RequireScope("deploy"))
```

`tokens.FromRequest(e)` returns the token a request used (nil for a JWT); `tokens.HasScope(e, "deploy")` is true for
JWTs and unscoped tokens. Plugins that don't import this package can read the request store keys `tokens.scopes` and
`tokens.id`.

## Go API

`tokens.For(app)` returns the service: `Create(owner, CreateOptions{Name, Scopes, Expires, ClientId})` → secret +
record, `Verify(secret)`, `List(owner)`, `Revoke(id)`, `RevokeAll(owner)`, `DeleteExpired()`, `RegisterScope`,
`Scopes()`. [oauth-server](../oauth-server/README.md) issues its access tokens through `Create`.

## License

MIT
