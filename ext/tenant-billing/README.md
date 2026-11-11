# tenant-billing

Billing for [tenants](../tenants/README.md), independent of any payment provider: plans and their quotas, the
provider hook that adapters implement (checkout links, webhooks, renewals, cancellations, refunds), applying a
subscription's plan to an account, and a reconcile job that compares accounts with the provider.

**Runs on:** [tenants](../tenants/README.md) (level 4). Providers are separate plugins, e.g.
[tenant-billing-ls](../tenant-billing-ls/README.md) for Lemon Squeezy.

> **Build it.** Nothing is published here yet. PocketHost's own billing (in its legacy Node mothership) is split
> between this plugin and the Lemon Squeezy adapter. See [Create your own plugin](../../docs/plugins.md).

## License

MIT
