# docker

An [edge](../edge/README.md) runtime that runs each instance in its own container with an official PocketBase binary
of the version it asks for, so one edge serves every PocketBase version next to [pocker](../pocker/README.md)'s
in-process instances.

**Runs on:** any host of many instances built on ext/edge (the `pocker` command, PocketHost's `pockethost edge`),
with a Docker daemon.

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/docker`, tested against a real Docker daemon
> (PocketBase 0.22 and 0.36 side by side). Not yet in production.

## How it runs an instance

- **Binary:** from a cache laid out as `<binaries>/<version>/<platform>/pocketbase` (PocketHost's existing layout);
  missing versions are downloaded from a manifest (`versions.json`: host + sha256 per platform) and checked. An
  instance's `version` may be exact (`0.22.21`) or a range (`0.22.*`, `~0.36`, `^0.30`); the newest match wins.
- **Container:** the instance folder at `/home/pockethost`, the binary read-only at `/home/pockethost/pocketbase`,
  `./pocketbase serve --http 0.0.0.0:8090` (plus `--dev`, and `--encryptionEnv=PB_ENCRYPTION_KEY` when the instance
  has an encryption key), the instance's secrets as environment, published on a loopback port the edge proxies to.
  Same layout as PocketHost's Node edge, so its instance folders move as they are. A host adds its own mounts,
  environment and labels with `Prepare`.
- **Lifecycle:** starts on the first request (ready when `/api/health` answers), at most `max_concurrent_starts` at
  once, stops after `idle_ttl` without requests, restarts on its next request when what it runs changed (secrets,
  version, mounts; a routing-only change doesn't restart it), stops when the instance is deleted or moves to another
  edge. Containers keep running across an edge restart: the next process adopts them by label (`adopt_image` also
  adopts unlabeled containers of an image, for taking over from another manager).
- **Limits and hardening** (optional): memory, CPUs, pids, nofile, dropped capabilities, no-new-privileges.
- **Logs:** container output goes to `OnLog` (default: the edge's debug log).

Instances choose it with `runtime: docker` (or a host's `ServesInProcess` rule, e.g. by version).

```yaml
docker:
  image: alpine:3.20
  binaries: /var/lib/pocketbase
  manifest: /etc/pocketbase/versions.json
  default_version: "0.22.*"
  idle_ttl: 10m
  max_concurrent_starts: 4
```

Tests: `go test ./...` runs the Docker tests when a daemon is reachable (skipped with `-short`); the first run
downloads two PocketBase releases into the user cache.

## License

MIT
