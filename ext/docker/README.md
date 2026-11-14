# docker

A runtime provider for [ext/host](../host/README.md): each instance runs in its own container with an official
PocketBase (or PocketOne) binary of the version it asks for, so one host serves every version. The peer of
[pocker](../pocker/README.md) (in-process); a host runs exactly one of them.

**Runs on:** [ext/host](../host/README.md), with a Docker daemon; add [ext/edge](../edge/README.md) for a fleet and
[ext/firewall](../firewall/README.md) for rate limits. `example/main.go` is a minimal docker host.

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/docker`, tested against a real Docker daemon
> (upstream PocketBase 0.23 and PocketOne 0.0.1 side by side, alone and as a host's runtime). Not yet in production.

## How it runs an instance

- **Releases:** a host serves an explicit list of releases, each pinned to its download and sha256, from a
  `VersionsSource`. Built in: `docker.versions` in pocketbase.yaml, the minors to serve (`["pb:0.36", "p1:0.0"]`).
  `docker update` finds the latest patch of each (upstream PocketBase from ganigeorgiev.com, then the archived GitHub;
  PocketOne from GitHub; checksums from each release's checksums.txt), records them in the cache's `releases.json`
  and downloads them; `docker seed` downloads whatever is listed but not cached. One function (`LatestPatches`) culls
  to the latest patch per minor and one (`Pick`) matches an instance's version, for every caller. A fleet's list can
  come from a mothership instead (a bridge implements `VersionsSource`).
- **Binary:** two release lines, picked by a prefix in the instance's `version`: `pb:` = upstream PocketBase (0.23
  and newer; 0.22 and older are sunset and refused), `p1:` = PocketOne; no prefix = `pb:`. The newest listed release
  that matches wins; anything not listed gets a clear 503. Cache: `<binaries>/<version>/<platform>/pocketbase`
  (PocketHost's existing layout) and `<binaries>/p1/<version>/<platform>/pocketone`. Listed releases missing from the
  cache are seeded in the background; there are no downloads at request time.
- **Container:** the instance folder at `/home/pockethost`, the binary read-only at `/home/pockethost/pocketbase`,
  `./pocketbase serve --http 0.0.0.0:8090` (plus `--dev`, and `--encryptionEnv=PB_ENCRYPTION_KEY` when the instance
  has an encryption key), the instance's secrets as environment, published on a loopback port the edge proxies to.
  Same layout as PocketHost's Node edge, so its instance folders move as they are. A host adds its own mounts,
  environment and labels with `Prepare`.
- **pb_hooks:** a change restarts just that container (PocketBase's HooksWatch, from the host, one watcher for all).
  Migrations run when a container boots.
- **Lifecycle:** starts on the first request (ready when `/api/health` answers), at most `max_concurrent_starts` at
  once, stops after `idle_ttl` without requests, restarts on its next request when what it runs changed (secrets,
  version, mounts; a routing-only change doesn't restart it), stops when the instance is deleted or moves to another
  edge. Containers keep running across an edge restart: the next process adopts them by label (`adopt_image` also
  adopts unlabeled containers of an image, for taking over from another manager).
- **Limits and hardening** (optional): CPUs, pids, nofile, dropped capabilities, no-new-privileges.
- **Logs:** container output goes to `OnLog` (default: the edge's debug log).

A host that runs docker sends every instance to it (one provider per process): `provider: docker` with a `docker`
section in pocketbase.yaml (`docker.Provider`; its `Container` hook adds host mounts/env, `Version` rewrites an
instance's version before the binary is picked).

```yaml
docker:
  image: alpine:3.20
  binaries: /var/lib/pocketbase
  versions: ["pb:0.23", "pb:0.36", "p1:0.0"]
  default_version: "pb:0.36.*"
  idle_ttl: 10m
  max_concurrent_starts: 4
```

Tests: `go test ./...` runs the Docker tests when a daemon is reachable (skipped with `-short`); the first run
downloads an upstream release and a PocketOne release into the user cache.

## License

MIT
