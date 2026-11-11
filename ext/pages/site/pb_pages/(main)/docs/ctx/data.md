---
title: ctx.data, ctx.params, ctx.route
description: The shared data object, route parameters and the matched route.
---

# `ctx.data`, `ctx.params`, `ctx.route`

## `ctx.data`

A plain object shared by middleware, handlers and templates. It starts as `{}`. In templates it's also available as `data`.

```js
// +middleware.js
module.exports = (ctx, next) => {
  ctx.data.user = ctx.auth
  return next()
}
```

```js
// +page.js
module.exports = {
  load(ctx) {
    return { products: ctx.app.findAllRecords('products') }  // Object.assign'd into ctx.data
  },
}
```

```ejs
<%% for (const p of data.products) { %>
  <li><%%= p.getString('name') %></li>
<%% } %>
```

A handler's returned object is shallow-assigned into `ctx.data`. There is no deep merge.

## `ctx.params`

Route parameters from `[name]` segments, as strings. Never includes query parameters (use [`ctx.req.query()`](/docs/ctx/req)). In templates it's also available as `params`.

```ejs
<!-- pb_pages/[org]/[repo]/index.ejs, at /acme/web -->
<p><%%= params.org %>/<%%= params.repo %></p>  <!-- acme/web -->
```

## `ctx.route`

`{ path, name, file }`: the route pattern, the page name and its file relative to `pb_pages`.

| Page file | `path` | `name` |
|---|---|---|
| `products/[id].ejs` | `/products/[id]` | `[id]` |
| `docs/index.md` | `/docs` | `index` |

A `+page.js` applies to every page in its folder; use `ctx.route.name` to tell them apart.
