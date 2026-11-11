---
title: Plugins
description: Built-in plugins (auth, realtime, datastar) and JS plugins, turned on in +config.js.
---

# Plugins

Plugins add features to `ctx` and hook into each request. Turn them on in `+config.js`:

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['auth', 'realtime', 'datastar'],
}
```

EJS and Markdown are always on; they aren't plugins.

## Built-in plugins

| Name | Adds |
|---|---|
| [`auth`](/docs/plugins/auth) | Cookie sessions on PocketBase auth: sets `ctx.auth` from the `pb_auth` cookie and adds `ctx.session.*` sign-in methods. |
| [`realtime`](/docs/plugins/realtime) | `ctx.realtime.send()` to PocketBase realtime subscribers. |
| [`datastar`](/docs/plugins/datastar) | `ctx.datastar.*` helpers for [Datastar](https://data-star.dev). |

## JS plugins

A JS plugin is a factory that returns hooks. Add it to `plugins` directly, or as `{ factory, ...options }`:

```js
module.exports = {
  plugins: ['auth', require('./_private/my-plugin.js')],
}
```

See [Plugin Authoring](/docs/plugins/authoring).

## Order

Plugins' hooks run in the order they're listed, and `onRequest` hooks run before any middleware.
