# bulkmail

Bulk mail: campaigns to an app's own users with one-click unsubscribe, bounce and complaint handling, and a dashboard page.

**Runs on:** Single app (in the standard `pocketbase` binary), or every instance under [pocker](../pocker/README.md)
through `pockerplugin`, which is also how the mothership and multitenant levels get it.

```go
srv.Plugins = append(srv.Plugins, pockerplugin.New(bulkmail.Config{})) // under pocker
```

Under pocker, process defaults come from the config file's top-level `bulkmail:` key and per-instance settings from
`plugins: {bulkmail: {enabled, rate, daily}}`; `daily: 0` means the instance may not send bulk mail.

## Use it

```go
app := pocketbase.New()
bulkmail.MustRegister(app, bulkmail.Config{Rate: 5, Daily: 5000})

// optional: $bulkmail in JS hooks
jsvm.MustRegister(app, jsvm.Config{OnInit: func(vm *goja.Runtime) { bulkmail.BindJS(vm, app) }})
```

```sh
go run ./example serve --dev  # test mail shows in the dev mail trap at http://127.0.0.1:8025
```

The dashboard gets a **Mail** page (campaigns, recipients, suppressions) and a **Bulk mail** section on Settings >
Mail (postal address, unsubscribe mailbox, bounce and complaint feedback). Campaigns send from the app's sender in
Settings > Mail. A campaign's `from_name` / `from_address` fields override it, but only through the records API, JS
or Go (the dashboard doesn't show them): the app's SMTP account can usually send only as identities it has verified.

## What it does

- **Audience** from the app's own data, resolved when sending starts: a collection or view plus a filter (the same
  syntax as API rules) and the field that holds the address (email or text, or a view's computed json column), or a
  read-only SQL `SELECT` that returns an `email` column. A view is the easy way to build a dynamic audience: it
  picks who gets mail and computes the columns templates use (`{{record.column}}`); the example creates one
  (`newsletter_audience`). SQL runs with SQLite's `query_only` on,
  inside a transaction that is always rolled back.
- **Templates:** `{{record.field}}` per recipient, `{{campaign.name}}`, `{{unsubscribe_url}}`, campaign vars (fixed
  values or `{"sql": "SELECT … AS value"}` computed once), and `{{if path}}…{{else}}…{{end}}` blocks. Values are
  HTML-escaped. An unsubscribe footer with the postal address is always added.
- **Headers:** `List-Unsubscribe` (signed HTTPS link, optional mailto) and `List-Unsubscribe-Post: One-Click`, as
  Gmail and Yahoo require for bulk senders.
- **Sending:** the audience is queued into a ledger first (one row per campaign and address, unique), then a
  background sender mails one message at a time through the app's mail client, at the lower of the app's rate and the
  campaign's. Restart-safe: a row becomes `sent` only after the server accepts it. SMTP 5xx fails the row; other
  errors retry three times with backoff. Starting a finished campaign again mails only new matches.
- **Limits and brakes:** per-second and per-day caps per app (`NoBulkMail` forbids sending). A campaign pauses itself
  when more than 5% of sends fail after the first 100, or on a spike of spam complaints.
- **Pause, resume, cancel, schedule, preview, test send** from the dashboard, the API or JS.
- **Unsubscribe:** signed links (HMAC with a per-app secret). Opening the link shows a confirm button, so link
  scanners can't unsubscribe anyone; the one-click POST from mail providers unsubscribes directly.
- **Bounces and complaints:** an Amazon SES endpoint for SNS (signatures verified, certificates only from
  `sns.<region>.amazonaws.com`, optional topic allow-list) and a generic webhook with a shared secret. Permanent
  bounces and complaints go on the suppression list, which wins over every audience.

## Data

| Collection | What |
|---|---|
| `_bulkmail_campaigns` | content, audience, vars, status, counters, schedule |
| `_bulkmail_sends` | the ledger: campaign, email, status (`queued`, `sent`, `skipped`, `failed`), reason, attempts |
| `_bulkmail_suppressions` | addresses that never receive campaign mail, with the reason |

All are superuser-only system collections with fixed collection and field ids (`schema.go`). Settings are a `_params`
row (`bulkmail`). `LegacyJSMigration()` emits a PocketBase JS migration that creates the same collections, for apps
that move onto this schema before they run this plugin.

## API (superuser)

| Route | |
|---|---|
| `GET`/`PATCH /api/bulkmail/settings` | settings (secrets are never returned), limits, feedback endpoint URLs |
| `POST /api/bulkmail/audience/count` | `{"audience": {...}}` → count, suppressed, sample |
| `POST /api/bulkmail/campaigns/{id}/start\|pause\|resume\|cancel` | campaign actions; `start` takes an optional `{"limit": N}` to queue only the next N new recipients |
| `POST /api/bulkmail/campaigns/{id}/preview` | rendered message for one recipient |
| `POST /api/bulkmail/campaigns/{id}/test` | send to `test_to` (no ledger rows) |

Campaigns and suppressions are created and edited through the regular records API.

Public: `GET`/`POST /api/bulkmail/unsubscribe?t=…`, `POST /api/bulkmail/feedback/ses`,
`POST /api/bulkmail/feedback/webhook`.

## Go and JS

- **Go:** `bulkmail.For(app)` returns the service: `Start`, `Pause`, `Resume`, `Cancel`, `Preview`, `Test`,
  `CountAudience`, `Suppress`, `Unsubscribe`, `UnsubscribeURL`, and the hooks `OnSend`, `OnUnsubscribe` and
  `OnFeedback`. `Config.Mailer` swaps the mail client, for example for a platform relay.
- **JS:** `$bulkmail.start(id, [limit])`, `.pause`, `.resume`, `.cancel`, `.test(id, [to])`, `.preview(id, [email])`,
  `.count(audience)`, `.suppress(email, [reason])`, `.unsuppress`, `.isSuppressed`, `.unsubscribe`, `.unsubscribeURL`,
  `.limits()`. React to activity with record hooks on the collections above, for example
  `onRecordAfterCreateSuccess((e) => { … }, "_bulkmail_suppressions")`.

## License

MIT
