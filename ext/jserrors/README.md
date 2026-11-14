# jserrors

JS hook errors that point at the `.pb.js` file they come from: `pb_hooks/main.pb.js:12:3` instead of
`/your/cwd/pb.js:1:40`.

**Runs on:** Single app, or many under [pocker](../pocker/README.md) (each instance's log). Ships in the standard
`pocketone` binary ([`examples/pocketone`](../../examples/pocketone/main.go)).

## Use

```go
jsvm.MustRegister(app, jsvm.Config{HooksDir: "pb_hooks", Extensions: []jsvm.Extension{jserrors.New()}})
```

It combines with other extensions, for example `Extensions: []jsvm.Extension{jsembed.Hooks(hooks), jserrors.New()}`
(embedded files are named `pb_hooks/<file>`). [`example/`](example/main.go): `cd example && go run . serve --dev`, then
`curl http://127.0.0.1:8090/boom` and read the log.

## What it does

jsvm serializes each hook handler (`fn.toString()`) and recompiles it as its own program under `<cwd>/pb.js`. That
name also anchors relative `require()` calls, so it can't change, and errors carry it. This plugin:

- remembers each hooks file while it loads, and where each handler (hook, route, middleware, cron) sits in it;
- rewrites `pb.js:<line>:<col>` in errors those handlers throw to `pb_hooks/<file>:<line>:<col>` (the wrapped error
  still unwraps to the original);
- names the file in errors raised while a hooks file itself runs.

A handler it can't place (built from a string, registered at runtime) keeps jsvm's message.

## No change to PocketBase's behavior

It's a jsvm extension (`jsvm.Config.Extensions`) with `OnLoad` and `OnCompile` methods, plus `jsvm.ScriptPath()`
(PocketOne's small seam; with no extensions, jsvm behaves like upstream). It used to live inside jsvm (`hooksource.go`); it moved here on
2026-11-13.
