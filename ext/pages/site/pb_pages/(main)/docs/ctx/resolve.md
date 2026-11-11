---
title: ctx.resolve
description: Load a module or file from a _private folder.
---

# `ctx.resolve(name, { mode }?)`

Loads a module from a `_private/` folder. The lookup is the same as [`include()`](/docs/partials): `_private/` in the current file's folder, then each parent folder up to `pb_pages/`. A name starting with `/` looks only in `pb_pages/_private/`.

```js
// pb_pages/_private/shout.js
module.exports = (s) => String(s).toUpperCase()
```

```ejs
<p><%%= ctx.resolve('shout')('hi') %></p>  <!-- HI -->
```

## Modes

| Mode | Returns |
|---|---|
| `require` (default) | The module's exports |
| `raw` | The file's text |
| `script` | The text wrapped in `<script>` |
| `style` | The text wrapped in `<style>` |

```ejs
<head>
  <%%- ctx.resolve('theme.css', { mode: 'style' }) %>
  <%%- ctx.resolve('widgets.js', { mode: 'script' }) %>
</head>
```

## Modules from `node_modules`

Page code can also `require()` packages from `pb_pages/node_modules`, then from the app's usual module paths. See [JavaScript Runtime](/docs/jsvm).
