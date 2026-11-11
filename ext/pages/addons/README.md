# pages add-ons

JS plugins for libraries that don't belong in `ext/pages` itself. Copy a file into your `pb_pages` (for example
`pb_pages/_private/`) and list it in `+config.js`:

```js
module.exports = { plugins: ['realtime', require('./_private/datastar.js')] }
```

| Add-on | What it adds |
|---|---|
| [datastar.js](datastar.js) | `ctx.datastar`: [Datastar](https://data-star.dev) script tags, SSE patches (`patchElements`, `patchSignals`, `executeScript`), signals, realtime broadcasts, and pages answering `Datastar-Request` with a patch. Its realtime helpers need the built-in `realtime` plugin. |

Starter kits and the PocketPages site carry copies; a test keeps them identical to the files here.
