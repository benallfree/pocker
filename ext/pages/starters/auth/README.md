# Auth starter

Multi-page app (MPA) auth with the built-in `auth` plugin: register, password, anonymous, OTP and OAuth2 login, logout, email verification and a protected `/account` section (`account/+middleware.js`). Sessions are a `pb_auth` cookie managed through `ctx.session.*`; the logged-in user is `ctx.auth`. The included migrations point PocketBase's mail at a local maildev and its email templates at this app's `/auth/confirm/{TOKEN}/...` pages.

```bash
pocketbase pages create myapp --starter auth
cd myapp
pocketbase serve
```

Then open http://127.0.0.1:8090.

In another terminal, run a local mail catcher for verification and OTP emails (web UI at http://localhost:1080):

```bash
npx maildev
```

Notes:

- Anonymous login needs password login enabled (PocketBase's default).
- OTP login is enabled by the migrations. OAuth2 is enabled with no providers: add one in the dashboard under Collections > users > Options > OAuth2.
