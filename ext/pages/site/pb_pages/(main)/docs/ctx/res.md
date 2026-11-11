---
title: ctx.res
description: The response - status, headers, cookies, redirects with flash messages, JSON, HTML, text, files and server-sent events.
---

# `ctx.res`

| Member | |
|---|---|
| `status(code)` | Status for the rendered page. Returns `ctx.res`. |
| `header(name, value)` | Set a response header. `header(name)` reads it back. |
| `cookie(name, value, options?)` | Set a cookie. |
| `clearCookie(name, options?)` | Expire a cookie (same `path` / `domain` as when set). |
| `redirect(location, { status = 302, flash }?)` | Send a redirect. `flash` sets a [one-time message](/docs/ctx/flash). |
| `json(data)` / `json(status, data)` | Send JSON. |
| `html(html)` / `html(status, html)` | Send HTML as-is (no layouts). |
| `text(text)` / `text(status, text)` | Send plain text. |
| `file(path)` | Send a file from `pb_pages` (path relative to `pb_pages`). |
| `sse()` | Start a `text/event-stream` response. |
| `sent` | `true` once a response has been sent. |

Once a response is sent, the later steps of the request (handlers, template, layouts) are skipped.

## Status and headers

```ejs
<%% ctx.res.status(404).header('Cache-Control', 'no-store') %>
<h1>Not here</h1>
```

## Redirects

```js
return ctx.res.redirect('/login')
return ctx.res.redirect('/account', { flash: 'Saved' })
return ctx.res.redirect('/new-home', { status: 301 })
```

## Cookies

```js
ctx.res.cookie('theme', 'dark', { maxAge: 60 * 60 * 24 * 365 })
ctx.res.cookie('prefs', { compact: true })   // objects are JSON.stringify'd
ctx.res.clearCookie('theme')
```

- Non-string values are `JSON.stringify`'d. The value is then percent-encoded (`encodeURIComponent`), and `ctx.req.cookie` decodes it.
- Defaults: `path=/`, `HttpOnly`, `SameSite=Lax`, and `Secure` when the request is HTTPS.
- Options: `path`, `domain`, `maxAge` (seconds), `expires` (Date), `httpOnly`, `secure`, `sameSite`.

## JSON, HTML, text and files

```js
return ctx.res.json({ ok: true })
return ctx.res.json(201, { id: record.id })
return ctx.res.html('<p>Fragment</p>')
return ctx.res.text(404, 'Not found')
return ctx.res.file('downloads/manual.pdf')
```

## Server-sent events

`sse()` starts a `text/event-stream` response and returns `{ send(event, data, { id, retry }?), comment(text), close() }`:

```js
// pb_pages/clock/+api.js
module.exports = {
  get(ctx) {
    const stream = ctx.res.sse()
    stream.send('time', new Date().toISOString())
    stream.close()
  },
}
```

Streaming is only available through `sse()`. Templates return their text all at once.
