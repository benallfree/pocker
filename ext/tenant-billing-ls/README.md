# tenant-billing-ls

Billing for [tenants](../tenants/README.md) with [Lemon Squeezy](https://www.lemonsqueezy.com): checkout links,
webhooks (signature-verified) that turn subscriptions into account quotas, renewals, cancellations and refunds, and a
reconcile job that compares accounts with Lemon Squeezy.

**Runs on:** [tenants](../tenants/README.md) (level 4). It implements the tenants plugin's billing provider hook, so
other providers can be plugins of their own.

> **Planned.** Nothing is published here yet. PocketHost's own Lemon Squeezy billing (in its legacy Node mothership)
> moves here. See [Create your own plugin](../../docs/plugins.md).

## License

MIT
