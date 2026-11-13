---
title: Moving from PocketPages 0.x
description: What changed between the PocketPages npm package (0.x) and PocketPages v1 in PocketOne.
---

# Moving from PocketPages 0.x

PocketPages v1 is part of PocketOne (the `pages` plugin). It keeps the ideas of the npm package (a folder of files that become routes, EJS, layouts, middleware, private partials) and changes the shapes. Apps written for 0.x don't run unchanged.

There is no migration tool. Start a new app with `pocketbase pages create <name>`, then move your pages over using the list below. The npm package is being retired.

## Folder and boot

| 0.x | v1 |
|---|---|
| `pb_hooks/pages/`, booted by `require('pocketpages')` in a `.pb.js` file | `pb_pages/` next to `pb_data`. No boot file, no npm install. |
| `create-pocketpages` | `pocketbase pages create <name> [--starter <kit>]` |

Delete the `.pb.js` boot file and the `pocketpages` dependencies, and move the contents of `pb_hooks/pages/` to `pb_pages/`.

## The context object

0.x passed an `api` object and spread its members into each template. v1 passes one `ctx` object with `req`, `res`, `data`, `params`, `route` and helpers. See the [ctx reference](/docs/ctx).

| 0.x | v1 |
|---|---|
| `request`, `response` | `ctx.req`, `ctx.res` |
| `body()`, `formData` | `ctx.req.body()` |
| `redirect(path)` | `ctx.res.redirect(path)` |
| `env()`, `store()`, `dbg()`, `info()`, `warn()`, `error()` | `ctx.env()`, `ctx.store()`, `ctx.log.debug()` / `info()` / `warn()` / `error()` |
| `auth`, `request.auth` | `ctx.auth` |

Inside templates, `data`, `params`, `asset` and `meta` are still available without the `ctx.` prefix.

## Parameters, query strings and cookies

- Query parameters are no longer mixed into `params`, and nothing is JSON-parsed for you. `ctx.params` holds route parameters only; read the query string with `ctx.req.query(name)`, which returns strings.
- Cookies are strings. Pass `{ json: true }` to parse one: `ctx.req.cookie('prefs', { json: true })`.

## Middleware

0.x middleware either returned a value or called `next(extra)`, depending on how many arguments it declared. v1 has one signature:

```js
module.exports = (ctx, next) => {
  ctx.data.user = ctx.auth
  return next()
}
```

Put shared data on `ctx.data`. See [Middleware](/docs/middleware).

## Loaders

`+load.js`, `+get.js`, `+post.js` and friends are replaced by one `+page.js` per folder that exports `load`, `get`, `post`, `put`, `patch` and `delete`. Returned objects are shallow-assigned into `ctx.data` (no deep merge).

```js
// 0.x: +load.js
module.exports = (api) => ({ products: $app.findAllRecords('products') })

// v1: +page.js
module.exports = {
  load(ctx) {
    return { products: ctx.app.findAllRecords('products') }
  },
}
```

See [Loading Data](/docs/loading-data).

## JSON

0.x sent template output as JSON if it parsed as JSON. v1 never does. Send JSON with `ctx.res.json()`, usually from a `+api.js` file. See [API Routes and JSON](/docs/api-routes).

## Layouts and slots

| 0.x | v1 |
|---|---|
| `<!-- slot:name -->` comments in the page | `slot('name', () => { %>...<%% })` |
| `slot` and `slots.name` in layouts | `slot()` and `slot('name')` |

See [Layouts and Slots](/docs/layouts).

## Output

`echo()` is gone. A template returns its text; anything you want in the output goes through a tag. For streaming, use `ctx.res.sse()`.

## Flash messages

0.x put the message in a `?__flash=` query parameter with `redirect(path, { message })`. v1 uses a one-time cookie:

```js
return ctx.res.redirect('/account', { flash: 'Saved' })
```

Read it on the next request with `ctx.flash()`. See [flash](/docs/ctx/flash).

## Assets

`asset()` URLs now carry the first 12 hex characters of the file's SHA-256, not the full hash.

## Configuration and plugins

- `+config.js` lists built-in plugins by name (`auth`, `realtime`, `datastar`) or JS plugin factories. npm plugin package names and EJS options are gone.
- EJS and Markdown are always on. `pocketpages-plugin-ejs` and `pocketpages-plugin-marked` have no v1 equivalent because they aren't needed.
- The auth plugin no longer goes through the JS SDK over HTTP (`api.pb()`). Use `ctx.session.*`, which calls the app directly. See [Authentication](/docs/plugins/auth).

See [+config.js](/docs/config) and [Plugins](/docs/plugins).

## Removed globals

| 0.x | v1 |
|---|---|
| `url()` | `ctx.req.url` |
| `stringify()` | `JSON.stringify` |
| micro-dash | plain JavaScript |
| `pb()` (JS SDK plugin) | `ctx.pb()`, built in: same SDK, no host setting, runs in-process as the signed-in user |

## Routing

A route with fewer segments no longer matches a longer URL. Segment counts must match: `products/[id].ejs` serves `/products/123` but not `/products/123/reviews`.
