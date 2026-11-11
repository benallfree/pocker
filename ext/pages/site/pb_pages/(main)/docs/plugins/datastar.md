---
title: Datastar Plugin
description: The built-in datastar plugin adds ctx.datastar helpers for Datastar script tags, SSE patches and signals.
---

# Datastar Plugin

The `datastar` plugin adds helpers for [Datastar](https://data-star.dev).

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['datastar'],
}
```

Add the script tags to your root layout's `<head>`:

```ejs
<head>
  <%%- ctx.datastar.scripts() %>
</head>
```

## `ctx.datastar`

| Member | |
|---|---|
| `scripts({ spa, realtime }?)` | Returns the script tags. |
| `patchElements(html, { selector, mode }?)` | Sends a patch-elements SSE event on the current response. |
| `patchSignals(signals)` | Sends a patch-signals SSE event. |
| `executeScript(js)` | Sends a script to run in the browser. |
| `signals()` | Reads the request's signals. |
| `realtime.*` | Sends the same patches to realtime subscribers. Needs the [`realtime`](/docs/plugins/realtime) plugin. |

## Pages as patches

When a request has the `Datastar-Request: true` header, the rendered page is sent without layouts, as a `patchElements` event. If the request has a `Datastar-Selector` header, the patch goes into that element.

This is how this site navigates between docs pages: the layout calls

```ejs
<%%- ctx.datastar.scripts({ spa: { scope: '#toc', selector: '#content' } }) %>
```

so links in the table of contents fetch the page and patch it into `#content`.

## Example: a counter

```ejs
<!-- pb_pages/counter/index.ejs -->
<button data-on-click="@post('/counter')">
  Count: <span id="count"><%%= ctx.store('count') || 0 %></span>
</button>
```

```js
// pb_pages/counter/+page.js
module.exports = {
  post(ctx) {
    const count = (ctx.store('count') || 0) + 1
    ctx.store('count', count)
    ctx.datastar.patchElements(`<span id="count">${count}</span>`)
  },
}
```

## Example: reading signals

```js
module.exports = {
  post(ctx) {
    const { name, email } = ctx.datastar.signals()
    if (!name || !email) {
      ctx.datastar.patchSignals({ error: 'Please fill all fields' })
      return
    }
    ctx.datastar.patchElements('<div id="form">Thanks!</div>')
  },
}
```

## Broadcasting

With the `realtime` plugin on, `ctx.datastar.realtime.*` sends patches to every realtime subscriber instead of the current response:

```js
// pb_pages/+config.js
module.exports = { plugins: ['realtime', 'datastar'] }
```

```js
ctx.datastar.realtime.patchElements(`<span id="count">${count}</span>`)
```
