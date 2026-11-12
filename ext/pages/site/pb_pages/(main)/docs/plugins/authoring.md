---
title: Plugin Authoring Guide
description: 'Write a JS plugin for PocketPages v1, a factory that returns onRequest, extendContext and transform hooks.'
---

# Plugin Authoring Guide

A JS plugin is a factory that returns hooks:

```js
// pb_pages/_private/my-plugin.js
module.exports = (config) => ({
  name: 'my-plugin',
  onRequest(ctx) {},                                   // before middleware
  extendContext(ctx) { ctx.hello = () => 'hi' },
  transform({ html, ctx, file }) { return html },      // after each template or layout renders
})
```

Turn it on in `+config.js`:

```js
module.exports = {
  plugins: [require('./_private/my-plugin.js')],
}
```

## `config`

`config` is the plugin's entry in `+config.js` when that entry is an object (`{ factory, ...options }`), else `{}`:

```js
module.exports = {
  plugins: [{ factory: require('./_private/banner.js'), text: 'Beta' }],
}
```

```js
// pb_pages/_private/banner.js
module.exports = (config) => ({
  name: 'banner',
  transform({ html }) {
    return html.replace('<body>', `<body><div class="banner">${config.text}</div>`)
  },
})
```

## Hooks

Every hook is optional. Hooks run in the order plugins are listed in `+config.js`.

| Hook | When |
|---|---|
| `name` | The plugin's name. |
| `onRequest(ctx)` | At the start of each request, before middleware. |
| `extendContext(ctx)` | Add members to `ctx`. |
| `transform({ html, ctx, file })` | After each template or layout renders. Return the new HTML. |

## Example: a context helper

```js
module.exports = () => ({
  name: 'format',
  extendContext(ctx) {
    ctx.formatDate = (d) => new Date(d).toLocaleDateString()
  },
})
```

```ejs
<p>Posted <%%= ctx.formatDate(data.post.getString('created')) %></p>
```

## Permissions

JS plugins run with the same permissions as the rest of the app's JS. Under a multitenant host, that means the instance's sandbox.
