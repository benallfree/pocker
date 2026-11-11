---
title: Authentication
description: Sign users in and out with the auth plugin - passwords, one-time passwords, OAuth2 and anonymous sessions.
---

# Authentication

PocketPages keeps PocketBase auth in a cookie with the built-in [`auth` plugin](/docs/plugins/auth). Turn it on:

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['auth'],
}
```

## How a request is authenticated

- If the request has PocketBase's own `Authorization` header, `ctx.auth` is that record.
- Otherwise, the `auth` plugin reads the `pb_auth` cookie, validates the token and sets `ctx.auth` (or clears a bad cookie).
- With neither, `ctx.auth` is `null`.

Every `ctx.session` sign-in method sets the cookie, sets `ctx.auth` for the rest of the request, and returns `{ token, record }`. `ctx.session.signOut()` clears it.

## Protecting pages

```js
// pb_pages/account/+middleware.js
module.exports = (ctx, next) => {
  if (!ctx.auth) return ctx.res.redirect('/login')
  return next()
}
```

```ejs
<%% if (ctx.auth) { %>
  <p>Welcome, <%%= ctx.auth.email() %></p>
  <form method="post" action="/logout"><button>Log out</button></form>
<%% } else { %>
  <a href="/login">Log in</a>
<%% } %>
```

## Passwords

```js
// pb_pages/login/+page.js
module.exports = {
  post(ctx) {
    const { email, password } = ctx.req.body()
    try {
      ctx.session.signInWithPassword(email, password)
    } catch (e) {
      return { error: e.message, email }
    }
    return ctx.res.redirect('/')
  },
}
```

```ejs
<!-- pb_pages/login/index.ejs -->
<form method="post">
  <label>Email</label>
  <input name="email" type="email" value="<%%= data.email %>" />
  <label>Password</label>
  <input name="password" type="password" />
  <button type="submit">Log in</button>
</form>
<%% if (data.error) { %><mark><%%= data.error %></mark><%% } %>
```

### Registration

```js
// pb_pages/register/+page.js
module.exports = {
  post(ctx) {
    const { email, password, name } = ctx.req.body()
    try {
      ctx.session.register(email, password, { data: { name } })
    } catch (e) {
      return { error: `Error registering: ${e.message}` }
    }
    return ctx.res.redirect('/', { flash: 'Welcome!' })
  },
}
```

## One-time passwords

A two-step form: request a code, then sign in with it. Turn on OTP for the collection in the PocketBase dashboard and set up mail (Settings > Mail settings).

```js
// pb_pages/login/otp/+page.js
module.exports = {
  post(ctx) {
    const { step, email, otpId, code } = ctx.req.body()
    try {
      if (step === 'request') {
        return { otpId: ctx.session.requestOTP(email), email }
      }
      ctx.session.signInWithOTP(otpId, code)
      return ctx.res.redirect('/', { flash: 'Signed in' })
    } catch (e) {
      return { error: e.message, otpId, email }
    }
  },
}
```

```ejs
<!-- pb_pages/login/otp/index.ejs -->
<%% if (data.error) { %><mark><%%= data.error %></mark><%% } %>

<%% if (!data.otpId) { %>
  <form method="post">
    <input type="hidden" name="step" value="request" />
    <input name="email" type="email" required />
    <button type="submit">Send code</button>
  </form>
<%% } else { %>
  <p>Check your email for the code.</p>
  <form method="post">
    <input type="hidden" name="step" value="confirm" />
    <input type="hidden" name="otpId" value="<%%= data.otpId %>" />
    <input name="code" required />
    <button type="submit">Sign in</button>
  </form>
<%% } %>
```

## OAuth2

Configure the provider in the PocketBase dashboard (Collections > users > Options > OAuth2). Then start the flow from a form:

```html
<form method="post" action="/auth/oauth">
  <input type="hidden" name="provider" value="google" />
  <button type="submit">Sign in with Google</button>
</form>
```

```js
// pb_pages/auth/oauth/+page.js
module.exports = {
  post(ctx) {
    ctx.session.requestOAuth2(ctx.req.body().provider)
  },
}
```

`requestOAuth2` stores its state in a cookie and redirects to the provider. The provider sends the user back to `/auth/oauth/confirm` (change it with `{ redirectPath }`):

```js
// pb_pages/auth/oauth/confirm/+page.js
module.exports = {
  load(ctx) {
    try {
      ctx.session.signInWithOAuth2(ctx.req.query('state'), ctx.req.query('code'))
    } catch (e) {
      return { error: e.message }
    }
    return ctx.res.redirect('/')
  },
}
```

```ejs
<!-- pb_pages/auth/oauth/confirm/index.ejs -->
<mark><%%= data.error %></mark>
```

See the [Google OAuth2 speedrun](/docs/speedruns/google-oauth2) for setting up Google.

## Anonymous sign-in

```js
// pb_pages/try/+page.js
module.exports = {
  post(ctx) {
    ctx.session.signInAnonymously()
    return ctx.res.redirect('/dashboard')
  },
}
```

## Your own auth flow

If you authenticate a record yourself, set the session cookie with `ctx.session.signIn(record)`.

## Signing out

```js
// pb_pages/logout/+page.js
module.exports = {
  post(ctx) {
    ctx.session.signOut()
    return ctx.res.redirect('/')
  },
}
```

## Security

1. Serve over HTTPS in production.
2. Turn on PocketBase's rate limiting.
3. Turn on email verification for your auth collection.
