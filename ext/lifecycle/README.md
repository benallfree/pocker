# lifecycle

Looks after the data of the instances a host serves: it measures facts about them and carries out decisions about
them.

**Runs on:** many instances, on an [ext/host](../host/README.md) host (pocker or docker).

> **Status.** Working, first step: facts (disk used, last request served) and the `measure` decision. Archiving,
> restoring, offloading backups and purging come next, as further decisions.

- **Facts** about each instance this host serves itself (not the ones it forwards): `disk_bytes` (its folder's size,
  0 without one) with when it was measured, and `last_active` (the last request this host served for it).
- **A policy provider** decides what to do. Built in: `YAMLPolicy`, rules from the `lifecycle` section, decided on
  this host. A control plane can provide its own (for example one that delegates the decisions to a mothership), and
  providers can wrap each other.
- **A sink** receives the facts that changed (default: a log line). A sink that also implements `Loader` gives the
  facts back at start, so a restart doesn't measure everything again.

```yaml
lifecycle:
  measure_every: 24h   # how old a disk measurement may get
  report_every: 1h     # how often changed last-request times are reported
  concurrency: 2       # instances measured at once
```

```go
lc := &lifecycle.Lifecycle{Directory: h.Directory, Folder: func(i host.Instance) string { return filepath.Join(root, i.ID) }}
h.Gates = append(h.Gates, lc)                // records each local instance's last request
h.Background = append(h.Background, lc.Run) // measures and reports on the policy's schedule
```

It knows nothing about plans, payments or tenants: that belongs to whichever policy provider is configured.

## License

MIT
