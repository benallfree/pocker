# mothership

A mothership and its edges: the control plane records which edges exist and which instances run where; each edge
follows it and serves its own instances, forwarding the rest to their owners.

One operator (level 3): owners and accounts are the [tenants](../tenants/README.md) plugin (level 4), which extends
the mothership through its schema and mirror extensions. Billing extends tenants. Each layer is optional: an
[edge](../edge/README.md) runs without a mothership, a mothership without tenants.

**Runs on:** a [host](../host/README.md) with the [edge](../edge/README.md) fleet layer.

> **Status.** Early: Go module `github.com/pockethost/pocketone/ext/mothership`. Built: the canonical `edges` and
> `instances` collections (`mothership.EnsureCollections`) and the edge side (`mirror`). Not yet: the mothership's own
> endpoints (placement, moves), instance status reports.

## The edge side: `mirror`

Follows the mothership over PocketBase's realtime stream (no polling) and serves the host and ext/edge as both their
`InstanceSource` and its `EdgeSource`. Each edge subscribes to filtered views, so it only receives what concerns it:

| View | Collection | Filter / fields | Why |
|---|---|---|---|
| `edges` | edges | all | forwarding, and health from heartbeats |
| `routes` | instances | fields `id,name,hosts,edge,updated` | the routing table: any edge may get any visitor |
| `local` | instances | `edge = <this edge>` | full records (secrets, limits, plugins) for instances this edge runs |

An instance moving away shows up in `routes` and its full record is dropped; one moving here arrives in `local`
(the old entry keeps forwarding until it does). The stream reconnects and reloads on its own, and reloads every 10
minutes in case anything was missed. The edge registers itself when it has no record (with `internal_url` set) and
marks itself online every 15 s (offline on shutdown).

**Extensions** (e.g. tenants) add views (`owners` filtered to `instances_via_owner.edge ?= {edge}`), adjust each
instance as it is built (block a suspended owner's instances, fold in the owner's trusted IPs) and name the
instances a change affects.

Filters cut what each edge downloads and keeps (most edges run a few of thousands of instances); they are not a
security boundary while edges log in as superusers.

```yaml
mothership:
  url: http://10.88.0.10:8090
  email: $MOTHERSHIP_EMAIL          # $VARS come from the private .env
  password: $MOTHERSHIP_PASSWORD
  self: lax-1                       # or edges.self
  internal_url: http://10.88.0.5:8080
  region: lax
  apex: example.com                 # <name>.example.com routes to each instance
```

The stock `pocketone` binary follows a mothership (instances and the other edges) when pocketone.yaml has this
section and names a provider.

## License

MIT
