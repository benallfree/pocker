---
title: Using Middleware in PocketPages
description: +middleware.js files run root first for every route below them, with one (ctx, next) signature.
---

# Middleware

Middleware files (`+middleware.js`) run before your handlers and templates. Use them to:

- Check authentication
- Load data a whole section shares
- Set headers
- Validate requests

## Basic example

```js
// pb_pages/admin/+middleware.js
module.exports = (ctx, next) => {
  if (!ctx.auth) return ctx.res.redirect('/login')
  ctx.data.section = 'admin'
  return next()
}
```

- One signature: `(ctx, next)`. Call `next()` to continue, and return its result.
- Returning without calling `next()` ends the request. If no response was sent, that's an error (a `500`; in dev mode the error page names the middleware file).
- Share data through `ctx.data`.

## Order

Every `+middleware.js` from `pb_pages/` down to the route's folder runs, root first. Groups count too.

```
pb_pages/
  +middleware.js         # runs first (all routes)
  products/
    +middleware.js       # runs second (/products/...)
    [id]/
      +middleware.js     # runs third (/products/[id])
```

The full order for a request is:

1. Plugins' `onRequest`
2. Middleware, root first
3. The folder's `+page.js` or `+api.js` (`load`, then the method's handler)
4. The template, then layouts, innermost first
5. The response

If any step sends a response, the later steps are skipped.

## Common uses

### Auth guard

```js
module.exports = (ctx, next) => {
  if (!ctx.auth) return ctx.res.redirect('/login')
  return next()
}
```

### Request validation

```js
// pb_pages/products/[id]/+middleware.js
module.exports = (ctx, next) => {
  if (!/^[a-z0-9]+$/.test(ctx.params.id)) throw ctx.httpError(400, 'Invalid ID')
  return next()
}
```

### Shared data

```js
// pb_pages/+middleware.js
module.exports = (ctx, next) => {
  ctx.data.categories = ctx.app.findRecordsByFilter('categories', 'active = true', 'name', 100)
  return next()
}
```

```ejs
<nav>
  <%% for (const c of data.categories) { %>
    <a href="/categories/<%%= c.id %>"><%%= c.getString('name') %></a>
  <%% } %>
</nav>
```

### Headers

```js
module.exports = (ctx, next) => {
  ctx.res.header('X-Frame-Options', 'DENY')
  return next()
}
```
