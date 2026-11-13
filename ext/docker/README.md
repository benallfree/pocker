# docker

An [edge](../edge/README.md) runtime that runs each instance in its own container with an official PocketBase binary
of the version it asks for, so one edge serves every PocketBase version next to [pocker](../pocker/README.md)'s
in-process instances.

**Runs on:** any host of many instances built on ext/edge (the `pocker` command, PocketHost's `pockethost edge`),
with a Docker daemon.

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/docker`, tested against a real Docker daemon
> (upstream PocketBase 0.23 and PBCE 0.0.1 side by side). Not yet in production.

## How it runs an instance

- **Binary:** two release lines, picked by a prefix in the instance's `version`: `pb:` = upstream PocketBase
  (0.23 and newer; 0.22 and older are sunset and refused), `ce:` = PBCE releases (their own version numbers);
  no prefix = `pb:`. Exact versions (`pb:0.36.9`) or ranges (`pb:0.36.*`, `ce:~0.0.1`, `^0.30`); the newest match
  wins. The cache is laid out as `<binaries>/<version>/<platform>/pocketbase` for upstream (PocketHost's existing
  layout) and `<binaries>/ce/<version>/<platform>/pocketbase` for PBCE; missing releases are downloaded from a
  manifest (`versions.json`: hosts, `versions` for upstream, `ce` for PBCE with each release's upstream base, sha256
  per platform) and checked.
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
  default_version: "pb:0.36.*"
  idle_ttl: 10m
  max_concurrent_starts: 4
```

Tests: `go test ./...` runs the Docker tests when a daemon is reachable (skipped with `-short`); the first run
downloads an upstream release and a PBCE release into the user cache.

## License

MIT
