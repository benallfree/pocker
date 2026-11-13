# config

PocketOne's config file: `pocketbase.yaml`, with secrets in a `.env` beside it. Linked by default in the `pocketbase`
binary.

> **Status.** Working: Go module `github.com/pockethost/pocketone/ext/config`. Sections today: `server`, `app`,
> `provider` (reserved for runtime providers). Plugins move their settings into their own sections next.

## Sections

Each plugin owns one top-level section, declared in its own package with a typed key:

```go
var Section = ceplugin.NewSection("firewall", Config{ /* defaults */ }, "doc…")

cfg, found := firewall.Section.Get()  // its defaults with the file's values applied
cfg, err := firewall.Section.Require() // fails when the file doesn't have it
```

A plugin reads a section only through its owner's key, so it can read only sections it imports; there is no lookup
by name and no access to the raw file. Startup fails on a top-level key no linked plugin owns (typos), a key defined
twice, or a value that doesn't decode. `pocketbase config` lists every section the binary knows, with docs and
defaults; `pocketbase config check` checks a file.

## Secrets

`$VAR` and `${VAR}` in values expand from the `.env`, then the process environment (`$$` is a literal `$`); an unset
name fails at startup. The `.env` is never written to the process environment, so JS hooks (`$os.getenv`) and
instances don't see it. In single-app mode, `app.env_allow` lists the `.env` names the app may read.

## Files

`--config` (default `./pocketbase.yaml`, else next to the executable) and `--env-file` (default `.env` beside the
config file). No file = stock behavior. Relative paths in the file are relative to the file's folder. Flags win over
the file.

```yaml
server:
  http: 127.0.0.1:8090
  origins: ["*"]
app:
  dir: ./pb_data
  hooks_dir: ./pb_hooks
  env_allow: [STRIPE_KEY]
```

## License

MIT
