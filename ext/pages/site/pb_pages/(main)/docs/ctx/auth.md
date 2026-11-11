---
title: ctx.auth
description: The authenticated record for the request, or null.
---

# `ctx.auth`

The authenticated record, or `null`. It's set by:

- PocketBase's own auth, when the request has an `Authorization` header;
- the [`auth` plugin](/docs/plugins/auth), from its `pb_auth` cookie.

```ejs
<%% if (ctx.auth) { %>
  <p>Welcome, <%%= ctx.auth.email() %></p>
  <%% if (!ctx.auth.verified()) { %><p>Please verify your email.</p><%% } %>
<%% } else { %>
  <a href="/login">Log in</a>
<%% } %>
```

`ctx.auth` is a PocketBase record, so the usual record methods work: `get(field)`, `getString(field)`, `email()`, `verified()`, `isSuperuser()`, `collection()`.

## Protecting pages

```js
// pb_pages/account/+middleware.js
module.exports = (ctx, next) => {
  if (!ctx.auth) return ctx.res.redirect('/login')
  return next()
}
```
