# tenant-billing-ls

The [Lemon Squeezy](https://www.lemonsqueezy.com) adapter for [tenant-billing](../tenant-billing/README.md): checkout
links, signature-verified webhooks mapped to subscriptions, renewals, cancellations and refunds, and the Lemon Squeezy
side of the reconcile job. Plans, quotas and applying them to accounts live in tenant-billing.

**Runs on:** [tenant-billing](../tenant-billing/README.md) (level 4). It implements tenant-billing's provider hook, so
other providers can be adapters of their own.

> **Build it.** Nothing is published here yet. PocketHost's own Lemon Squeezy billing (in its legacy Node mothership)
> moves here. See [Create your own plugin](../../docs/plugins.md).

## License

MIT
