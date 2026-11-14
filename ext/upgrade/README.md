# upgrade

`pocketone upgrade`: replaces the running executable with the latest PocketOne release and keeps the old one beside
it as `<name>-<version>` (e.g. `pocketone-0.0.3`). Linked by default in the `pocketone` binary.

## What it does

- Downloads the archive for your platform from the latest GitHub release and checks it against the release's
  `checksums.txt` before touching anything.
- Renames the current executable to `<name>-<version>` and puts the new one in its place. **Old copies are never
  deleted**, so `pocketone-0.0.3 serve` still runs a project on the engine it was built with. A local build with no
  release version is kept as `<name>.old`.
- Backs up `pb_data` afterwards when the current folder has one (`--backup=false` skips it).
- Installed with Homebrew: prints `brew upgrade pocketone` instead (brew keeps old versions until `brew cleanup`).
- In a Docker container: refuses; upgrade the image instead.

A kept binary doesn't undo migrations: once a newer version has served a `pb_data`, an older one may not open it.
The backup is the way back for that.

## Use

```go
upgrade.MustRegister(app, app.RootCmd, upgrade.Config{})
```

Custom builds that publish their own releases set `Owner`, `Repo`, `ArchivePrefix` and `ArchiveExecutable`.
`Config.FetchRelease` and `Config.DownloadArchive` are exported for other tools that fetch a specific release.

## License

MIT
