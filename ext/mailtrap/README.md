# mailtrap

A development mail catcher (like MailDev or Mailpit): in dev mode, an app whose SMTP is off sends its mail here
instead of the host's `sendmail`, and you read it at http://127.0.0.1:8025.

**Runs on:** Single app, or many under [pocker](../pocker/README.md) (one inbox for every dev instance, labeled by
instance). Ships in the standard `pocketbase` binary.

## What it does

- Only in dev mode (`serve --dev`, or a dev instance under pocker) and only while the app's SMTP is off. With SMTP on,
  or outside dev mode, it does nothing.
- Catches every message the app sends (verification, password reset, `$app.newMailClient()`, bulk mail) after the
  app's own `onMailerSend` hooks, and never calls the next handler, so `sendmail` never runs.
- A web inbox at http://127.0.0.1:8025 (HTML, text, raw source), and an SMTP listener at 127.0.0.1:1025 that accepts
  anything, for other tools (for example [mailrelay](../mailrelay/README.md)'s example uses it as the relay).
- Prints the inbox address at startup and one line per caught message.
- Keeps the last 500 messages in memory. If its ports are taken, it still catches mail and prints each one.

Never expose its ports: anyone who can reach them can read the mail.

## Use

```go
app := pocketbase.New()
mailtrap.MustRegister(app)
```

Under pocker, add `mailtrap.New()` to `Server.Plugins`.

| Variable | Default |
|---|---|
| `PB_MAILTRAP_HTTP` | `127.0.0.1:8025` |
| `PB_MAILTRAP_SMTP` | `127.0.0.1:1025` |

## No change to PocketBase

It's built on PocketBase's own `OnMailerSend` hook (the same way [mailrelay](../mailrelay/README.md) takes over an
instance's mail), so core is unchanged. It used to live in core (`tools/mailer/trap.go`); it moved here on 2026-11-13.
