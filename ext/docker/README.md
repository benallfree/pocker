# docker

A runtime provider for [ext/host](../host/README.md): each instance runs in its own container with an official
PocketBase (or PocketOne) binary of the version it asks for, so one host serves every version. The peer of
[pocker](../pocker/README.md) (in-process); a host runs exactly one of them.

**Runs on:** [ext/host](../host/README.md), with a Docker daemon; add [ext/edge](../edge/README.md) for a fleet and
[ext/firewall](../firewall/README.md) for rate limits. `example/main.go` is a minimal docker host.

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/docker`, tested against a real Docker daemon
> (upstream PocketBase 0.23 and PocketOne 0.0.1 side by side, alone and as a host's runtime). Not yet in production.

## How it runs an instance

- **Binary:** two release lines, picked by a prefix in the instance's `version`: `pb:` = upstream PocketBase
  (0.23 and newer; 0.22 and older are sunset and refused), `p1:` = PocketOne releases (their own version numbers);
  no prefix = `pb:`. Exact versions (`pb:0.36.9`) or ranges (`pb:0.36.*`, `p1:~0.0.1`, `^0.30`); the newest match
  wins. The cache is laid out as `<binaries>/<version>/<platform>/pocketbase` for upstream (PocketHost's existing
  layout) and `<binaries>/p1/<version>/<platform>/pocketone` for PocketOne; missing releases are downloaded from a
  manifest (`versions.json`: hosts, `versions` for upstream, `pocketone` for PocketOne with each release's upstream base, sha256
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

A host that runs docker sends every instance to it (one provider per process): `provider: docker` with a `docker`
section in pocketbase.yaml (`docker.Provider`; its `Container` hook adds host mounts/env, `Version` rewrites an
instance's version before the binary is picked).

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
downloads an upstream release and a PocketOne release into the user cache.

## License

MIT
