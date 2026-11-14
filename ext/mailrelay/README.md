# mailrelay

Outgoing mail for [Pocker](../pocker/README.md) instances that haven't set up SMTP.

Stock PocketBase falls back to the host's `sendmail` when SMTP is off. Under Pocker that would run `sendmail` on the
shared host for every tenant. With this plugin:

- **SMTP on** in an instance's Settings → Mail: PocketBase sends the mail itself. Bring your own SMTP always wins.
- **SMTP off:** the mail goes through the operator's relay as `<instance>@<Domain>`, within per-instance limits.
  `sendmail` never runs.

Runs on: **Pocker**. It's a PocketOne plugin ([`pluginkit`](../pluginkit)), so it registers on a single app too, but
a single app can configure its own SMTP and doesn't need a relay.

## How it works

- The plugin binds PocketBase's `OnMailerSend` on every instance. No core change.
- It runs last, after the app's own `onMailerSend` hooks.
- When SMTP is off it doesn't call `e.Next()`, so PocketBase's `sendmail` fallback never runs. It relays the message
  in-process instead.
- Nothing is written to the instance's settings. A backup restored somewhere else has no relay credentials in it, and
  falls back to stock PocketBase behavior.

For each message:

1. **Check it's allowed.** May this instance send (`send`)? Are there no more than 5 recipients (To + Cc +
   Bcc)? Is the encoded size no more than 1 MiB?
2. **Count it.** Each message counts once against the instance's limits, however many recipients it has. Defaults are
   20 a minute and 200 a day, and the day resets at midnight UTC.
3. **Set the sender.** The From address becomes `<local part>@<Domain>`. The display name is kept, minus anything
   that looks like an address or a link. The app's own sender address becomes `Reply-To`, unless it's PocketBase's
   `@example.com` placeholder.
4. **Build and send.** Bcc stays out of the headers. An `X-Pocker-Instance` header is added, then the message goes to
   the `Upstream`.
5. **Log it.** One line goes to the instance's own log, with masked addresses: `mail from blog@… to a***@x.org
   accepted`.

A refused message returns an error to whoever triggered the mail, such as a password-reset request.

The instance's superusers see the relay on Settings → Mail, and `GET /api/mailrelay` (superuser only) returns the
same status.

## Use

```go
srv.Plugins = append(srv.Plugins, mailrelay.New(mailrelay.Config{
    Domain:   "mail.example.com",
    Upstream: mailrelay.SMTPUpstream{Addr: "smtp.provider.com:587", Username: "…", Password: "…"},
}))
```

- **`Upstream`** is any `Send(ctx, from, to, raw)`. `SMTPUpstream` ships here. It dials with its own dialer, so
  Pocker's egress rules for tenants (public addresses only) don't block a private relay. An API sender such as SES
  `SendRawEmail` fits the same interface.
- **`LocalPart`** picks an instance's address from its id (`pluginkit.AppID`). The default is the first label of the
  id: `blog.localhost` → `blog`.

`pocketone.yaml` (process defaults; limits set in Go win):

```yaml
mailrelay: { per_minute: 20, daily: 200 }
```

Per instance, set in `instances.yaml` or by your instance source (for example, per plan):

```yaml
plugins:
  mailrelay: { send: false }           # refuse this instance's mail unless it uses its own SMTP
  mailrelay: { per_minute: 60, daily: 2000 }
```

A change to these settings restarts the instance's app on its next request.

Use `send: false`, not `enabled: false`, to stop an instance sending. `enabled: false` turns the plugin off for that
instance, and with SMTP off PocketBase then falls back to the host's `sendmail`. Don't set it under Pocker.

## Try it: `example/` with the dev mail trap

The dev mail trap ([`ext/mailtrap`](../mailtrap/README.md), in the standard binary for `serve --dev`) is an in-process mail
catcher, like MailDev or Mailpit, with an SMTP port and a web inbox. The example points the relay's upstream at its
SMTP port, so relayed mail goes through the real SMTP path and lands in the inbox:

```bash
go run ./example --dev
```

```bash
curl -X POST http://alpha.localhost:8090/api/collections/users/request-password-reset -d email=user@example.com
```

```bash
curl -X POST http://beta.localhost:8090/api/collections/users/request-password-reset -d email=user@example.com
```

- Open http://127.0.0.1:8025. Every instance's mail is in one inbox, tagged with the instance, and you can filter by
  instance.
- Each instance's dashboard shows the relay on Settings → Mail. The example's logins are the shared test accounts
  ([`ext/devusers`](../devusers)): `admin@example.com` / `password`, and the user `user@example.com` / `password`.
- `--listen` changes the instances' port. `PB_MAILTRAP_HTTP` and `PB_MAILTRAP_SMTP` change the trap's.
- Without mailrelay, the stock `pocketone` binary in host dev mode (which includes `mailtrap`) already sends every
  instance's SMTP-less mail to the same trap, as is, with no relay rules.

The trap only runs in dev mode. It accepts mail from anyone who can reach its port, so never expose it.
