# tenants

Accounts that own instances, for hosting other people (level 4): each instance has an owner; accounts have quotas and
can be suspended; owners log in to their instances' dashboards (their login is kept in sync as a superuser); owners
get API tokens for the CLI and deploys; and an API your own tenant dashboard talks to (PocketHost's dashboard is one).

**Runs on:** [mothership](../mothership/README.md) (level 3) and [pocker](../pocker/README.md); requires the
[sandbox](../sandbox/README.md), since tenants run code you don't control.

**How it fits with other plugins:**

- **Mothership:** adds an `owner` to instances and an accounts table through the mothership's schema hooks, and adds
  owner-derived values (superusers, blocked status, plugin settings) to what edges receive through its feed hooks.
- **Settings for other plugins:** written into each instance's `plugins:` block, e.g. an owner's trusted IPs for the
  [firewall](../firewall/README.md) or a plan's retention for [pitr](../pitr/README.md). Those plugins don't know
  tenants exist.
- **Deploy:** supplies the tenant-token backend for the [deploy](../deploy/README.md) API.
- **Billing:** a billing provider hook; [tenant-billing-ls](../tenant-billing-ls/README.md) is the Lemon Squeezy one.

> **Planned.** Nothing is published here yet. See [Create your own plugin](../../docs/plugins.md).

## License

MIT
