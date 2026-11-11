---
title: The ctx Object
description: Reference for ctx, the one object per request passed to middleware, handlers, templates and plugins.
---

# The `ctx` Object

PocketPages creates one `ctx` object per request and passes it to middleware, handlers, templates and plugins.

```js
// +middleware.js
module.exports = (ctx, next) => next()

// +page.js / +api.js
module.exports = { load(ctx) {} }
```

```ejs
<!-- any template -->
<p><%%= ctx.req.url.pathname %></p>
```

In templates, `data`, `params`, `asset` and `meta` are shortcuts for `ctx.data`, `ctx.params`, `ctx.asset` and `ctx.meta`.

## Members

| Member | |
|---|---|
| [`ctx.req`](/docs/ctx/req) | The request: method, URL, query, headers, cookies, body, files |
| [`ctx.res`](/docs/ctx/res) | The response: status, headers, cookies, redirects, JSON, HTML, text, files, SSE |
| [`ctx.data`](/docs/ctx/data) | Plain object shared by middleware, handlers and templates |
| [`ctx.params`](/docs/ctx/data#ctxparams) | Route parameters |
| [`ctx.route`](/docs/ctx/data#ctxroute) | The matched route: `{ path, name, file }` |
| [`ctx.flash()`](/docs/ctx/flash) | The one-time message from the previous redirect |
| [`ctx.auth`](/docs/ctx/auth) | The authenticated record, or `null` |
| [`ctx.app`](/docs/ctx/app) | The app (`$app`) |
| [`ctx.pb()`](/docs/ctx/pb) | A JS SDK client acting as the visitor (API rules apply) |
| [`ctx.env(name)`](/docs/ctx/env) | Environment variable |
| [`ctx.log`](/docs/ctx/log) | `debug`, `info`, `warn`, `error` |
| [`ctx.meta(key, value?)`](/docs/ctx/meta) | Per-request page metadata |
| [`ctx.asset(path)`](/docs/ctx/asset) | Fingerprinted URL for a static file |
| [`ctx.resolve(name, { mode }?)`](/docs/ctx/resolve) | Load a module from `_private/` |
| [`ctx.store(name, value?)`](/docs/ctx/store) | The app's in-memory store |
| `ctx.dev` | `true` in dev mode |
| [`ctx.httpError(status, message)`](/docs/errors) | An error to throw for a specific status |
| [`ctx.error`](/docs/errors) | `{ status, message }`, in `+error.ejs` |

## Added by plugins

| Member | Plugin |
|---|---|
| `ctx.session` | [auth](/docs/plugins/auth) |
| `ctx.realtime` | [realtime](/docs/plugins/realtime) |
| `ctx.datastar` | [datastar](/docs/plugins/datastar) |

JS plugins can add their own members with [`extendContext`](/docs/plugins/authoring).
