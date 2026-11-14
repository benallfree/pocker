# PocketOne

PocketOne is PocketBase plus plugins. **PocketBase's own code ("core") changes only for hooks that let `ext/`
extend or override behavior; the features themselves live in `ext/`.** Core is every path outside what PocketOne owns:
`ext/`, `examples/pocketone/`, `docs/`, `README.md`, `AGENTS.md`, `CLAUDE.md`, `.github/`, `.claude/`. Each core change is marked
`// PocketOne:` so upstream merges stay easy.

## Every core change gets an independent audit before it reaches main

Any commit to `main` that changes core must pass an audit by a reviewer who didn't write it (a fresh agent with no
context from the change's session, or a human), and must record the result in a `Core-Audit:` commit trailer. CI
(`.github/workflows/core-audit.yml`) fails commits that change core without one.

1. Get the core diff: `.github/core-audit/core-diff.sh origin/main HEAD` (or `<sha>^ <sha>` for one commit).
2. Give the reviewer only that diff, this section, and the commit message. For each changed hunk it answers:
   - **Necessary:** could this live in `ext/` instead, using hooks core already has (upstream's own, or PocketOne's existing
     seams such as `OnFilesystemCreate`, `jsvm.Config.Extensions`, `BaseAppConfig.AppMigrations`/`OnLog`)?
   - **Minimal:** is it the smallest seam that works, with no feature logic, policy or plugin-specific names in core?
   - **Sensible:** marked `// PocketOne:`, documented on the hook, upstream behavior unchanged when nothing uses it, tested?
3. Pass: add the trailer and push. Fail: move the logic to `ext/` or shrink the seam, then audit again.

Trailer forms (the last line of the commit message, after a blank line):

```
Core-Audit: pass - <one line: what the core change is and why it can't live in ext/>
Core-Audit: upstream-merge v0.x.y
```

`upstream-merge` is only for merging a PocketBase release; resolve conflicts in the `// PocketOne:` spots and audit any
PocketOne change the merge forced.

## Core changes that aren't hooks

Bug fixes to upstream behavior, each tested and meant for upstream; drop each one once upstream has it:

- `apis/middlewares.go` `logRequest`: logs the status the client actually got for a plain error, not 0.
- `apis/realtime.go` `realtimeCanAccessRecord`: a superuser's subscription filter may use hidden fields, as in the
  list API (`TestRealtimeSuperuserFilterOnHiddenField`).
- `core/notify_watcher.go`: keeps the notify file until the next write, so macOS (kqueue) sees the change.

## Other rules

- Plugins are their own Go modules under `ext/<name>/` with a README and a minimal `example/main.go`
  (`docs/plugins.md`). Modules that reach another `ext/` module add a `replace` for it.
- Plugin CI (`.github/workflows/plugins.yml`, tool in `.github/plugins/`): every `pluginkit.Plugin` module has a
  conformance test, and each module a change affects builds, vets and passes `go test -race`. Check locally with
  `go run ./.github/plugins check` and `go run ./.github/plugins affected origin/main HEAD`.
- Build checks: `go build -o /dev/null ./...` (a plain `go build` in a single-main module writes a binary into the
  tree). Stage files by name; never commit built binaries.
- Plans and internal notes go in a private maintainer repo, never here.
