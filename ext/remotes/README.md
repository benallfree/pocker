# remotes

Named remotes for the `pocketbase` command: the instances a project deploys to, signed in once.

```
pocketbase remote add prod https://myapp.example.com
pocketbase login prod
pocketbase deploy prod          # ext/deploy
```

**Runs on:** the `pocketbase` command (client) and any app (server half: `GET /_pocker/whoami`).

```go
remotes.MustRegister(app, remotes.Config{}) // whoami route; on *pocketbase.PocketBase also the commands
```

Try it: `go run ./example serve` (logins from [devusers](../devusers/devusers.go): admin@example.com / password).

## Commands

- `remote add <name> <url> [--default]`, `remote list`, `remote rm <name>`, `remote default <name>`,
  `remote whoami [name]`.
- `login [remote]`: in the browser when the remote offers OAuth (`--device` for a code on another device,
  `--no-browser` to print the URL), else a pasted token (`--token`, `--token-stdin`), else email + password
  (`--email`, `--collection`; `POCKETBASE_PASSWORD` skips the prompt; a one-time code is asked for when the account has
  MFA). A password sign-in mints an API token named "pocketbase CLI on <host>" (logout revokes it), so the remote
  needs [tokens](../tokens/README.md); the session itself is never saved.
- `logout [remote]`, `link <url> [token] [--name default]` (= `remote add` + `login`).

Browser login runs the OAuth client against [oauth-server](../oauth-server/README.md): a loopback listener on
127.0.0.1 with PKCE, or a device code when there's no browser here (SSH, no display) or with `--device`. The login
keeps a refresh token and renews itself a day before it expires; `logout` revokes it on the remote.

A remote name can also be a URL (`pocketbase deploy https://other.example.com`).

## Files

- `.pocketbase/remotes.json` in the project (commit it): names and URLs only.
  `{"default": "prod", "remotes": {"prod": {"url": "https://myapp.example.com"}}}`. Commands find it by walking up
  from the current folder. Not a `pb_*` name, since `pb_*` folders are deployed.
- An existing `.pockethostconfig` (`{"instanceName": "myapp"}`, from `npx pockethost link`) reads as the remote
  `pockethost` → `https://myapp.pockethost.io`.
- `~/.config/pocketbase/credentials.json` (0600; `$POCKETBASE_CONFIG_DIR` or `$XDG_CONFIG_HOME` move it): tokens
  keyed by URL.
- CI: `POCKETBASE_TOKEN_<REMOTE>` (upper case, other characters as `_`), then `POCKETBASE_TOKEN`; for the
  `pockethost` remote also `POCKETHOST_TOKEN`. The environment wins over a saved login.

## Server half: `GET /_pocker/whoami`

Answers who the request's credential is and what the instance offers, so clients can discover features:

```json
{"auth": {"collection": "_superusers", "id": "…", "email": "admin@example.com"}, "via": "token",
 "tokenId": "…", "scopes": ["deploy"], "features": {"deploy": "/_pocker/deploy/v1", "tokens": "/api/tokens"},
 "app": {"name": "Acme", "url": "https://myapp.example.com"}}
```

Every API token may call it, whatever its scopes. Plugins list themselves with `remotes.Advertise(app, "deploy",
"/_pocker/deploy/v1")`; the tokens plugin is detected without importing it.

## Go API

`remotes.Open(dir, name)` resolves a remote and its credential and returns a `*remotes.Client` (base URL, bearer
token, HTTP client with short connect timeouts; `JSON`, `NewRequest`, `Do`, `Whoami`). Other client commands
([deploy](../deploy/README.md)) build on it. `remotes.BrowserLogin` is the hook the OAuth client fills in.

## License

MIT
