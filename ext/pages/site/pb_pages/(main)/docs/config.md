---
title: Configuration with +config.js
description: Turn on built-in plugins, add JS plugins, and enable debug output from pb_pages/+config.js.
---

# Configuration with `+config.js`

Create `pb_pages/+config.js` to configure PocketPages for the app:

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['auth', 'datastar', require('./_private/my-plugin.js')],
  debug: false,
}
```

## Options

| Key | Default | |
|---|---|---|
| `plugins` | `[]` | Built-in plugins by name, or JS plugin factories. EJS and Markdown are always on. |
| `debug` | `false` | Detailed error pages and `ctx.log.debug` output even outside dev mode. |

Unknown keys, unknown built-in names and duplicates are startup errors.

## Plugins

Each entry in `plugins` is one of:

- **A built-in name**: `'auth'`, `'realtime'` or `'datastar'`. See [Plugins](/docs/plugins).
- **A JS plugin factory**: a function that returns hooks.
- **An object with a factory and options**: `{ factory, ...options }`. The object is passed to the factory as its `config`.

```js
module.exports = {
  plugins: [
    'auth',
    'realtime',
    { factory: require('./_private/banner.js'), text: 'Hello' },
  ],
}
```

Plugin hooks run in the order the plugins are listed. See [Plugin Authoring](/docs/plugins/authoring).

## Debug

`debug: true` turns on what dev mode (`--dev`) turns on: detailed error pages and `ctx.log.debug` output. See [Debugging](/docs/debugging).
