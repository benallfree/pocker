---
title: Datastar Plugin
description: The Datastar add-on adds ctx.datastar helpers for Datastar script tags, SSE patches and signals.
---

# Datastar Plugin

The Datastar add-on adds helpers for [Datastar](https://data-star.dev). It's a JS plugin that ships with PocketPages
rather than a built-in: copy [`addons/datastar.js`](https://github.com/pockethost/pocketone/blob/main/ext/pages/addons/datastar.js)
into `pb_pages/_private/` (the `datastar` starter kit already has it) and list it in `+config.js`:

```js
// pb_pages/+config.js
module.exports = {
  plugins: [require('./_private/datastar.js')],
}
```

Add the script tags to your root layout's `<head>`. They load [Datastar](https://data-star.dev) v1.0.4. Datastar 1.0 uses
a colon between the attribute name and its key (`data-on:click`, `data-bind:name`, `data-signals:count`,
`data-class:active`, `data-attr:href`); the older hyphen form (`data-on-click`) is ignored. See the
[Datastar attribute reference](https://data-star.dev/reference/attributes).

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

so links in the table of contents fetch the page and patch it into `#content`. Plain left-clicks are intercepted;
modified clicks (Cmd/Ctrl/Shift/Alt, middle-click), links with a `target` attribute and links to other origins navigate
normally. Back/Forward work, including back to the page you landed on: each page opens at the top, and Back/Forward
return you to where you were scrolled.

## Example: a counter

```ejs
<!-- pb_pages/counter/index.ejs -->
<button data-on:click="@post('/counter')">
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
