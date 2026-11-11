# Datastar starter

A realtime chat with rooms and a live counter, built with [Datastar](https://data-star.dev/) and the built-in `datastar` and `realtime` plugins. Datastar's `@get` calls hit `/fragments/chat` and `/fragments/count`; because those requests carry `Datastar-Request: true`, the rendered page comes back as a `patchElements` event. A chat message is also broadcast to every tab in the same room with `ctx.datastar.realtime.patchElements`, and `ctx.datastar.patchSignals` clears the input. Open two tabs to watch it update.

```bash
pocketbase pages create myapp --starter datastar
cd myapp
pocketbase serve --dir ./pb_data
```

Then open http://127.0.0.1:8090.
