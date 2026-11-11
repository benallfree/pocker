---
title: Authentication Plugin
description: The built-in auth plugin keeps PocketBase auth in a cookie and adds ctx.session sign-in methods.
---

# Authentication Plugin

The `auth` plugin gives you cookie sessions on PocketBase auth. It calls the app directly, not over HTTP.

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['auth'],
}
```

## What it does on every request

It reads the `pb_auth` cookie, validates the token and sets `ctx.auth` to the authenticated record. A bad cookie is cleared.

`ctx.auth` is also set without the plugin when a request carries PocketBase's own `Authorization` header.

## `ctx.session`

| Method | |
|---|---|
| `register(email, password, { collection = 'users', data }?)` | Create an auth record and sign in. `data` holds extra fields. |
| `signInWithPassword(email, password, { collection }?)` | Sign in with a password. |
| `requestOTP(email, { collection }?)` | Send a one-time password. Returns the `otpId`. |
| `signInWithOTP(otpId, code, { collection }?)` | Sign in with a one-time password. |
| `signInAnonymously({ collection }?)` | Sign in as a new anonymous user. |
| `requestOAuth2(provider, { collection, redirectPath = '/auth/oauth/confirm' }?)` | Store state in a cookie and redirect to the provider. |
| `signInWithOAuth2(state, code)` | Complete an OAuth2 sign-in on the redirect path. |
| `signIn(record)` | Set the cookie for a record you authenticated yourself. |
| `signOut()` | Sign out. |

Every sign-in method sets `ctx.auth` for the rest of the request and returns `{ token, record }`.

## Example

```js
// pb_pages/login/+page.js
module.exports = {
  post(ctx) {
    const { email, password } = ctx.req.body()
    try {
      ctx.session.signInWithPassword(email, password)
    } catch (e) {
      return { error: 'Invalid email or password', email }
    }
    return ctx.res.redirect('/')
  },
}
```

```js
// pb_pages/logout/+page.js
module.exports = {
  post(ctx) {
    ctx.session.signOut()
    return ctx.res.redirect('/')
  },
}
```

See the [Authentication guide](/docs/authentication) for complete flows: registration, one-time passwords, OAuth2 and anonymous sign-in.
