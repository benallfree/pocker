---
title: ctx.req
description: The request - method, URL, query parameters, headers, cookies, body, uploaded files and client IP.
---

# `ctx.req`

| Member | |
|---|---|
| `method` | `"GET"`, `"POST"`, ... |
| `url` | `{ href, origin, pathname, search }` |
| `query(name)` | First value of a query parameter as a string, or `undefined` |
| `query(name, { all: true })` | Every value, as an array of strings |
| `query()` | `{ name: firstValue }` for every parameter |
| `header(name)` | Request header, `''` if absent |
| `cookie(name)` | Cookie value as a string, or `undefined` |
| `cookie(name, { json: true })` | Cookie parsed as JSON (`undefined` if absent or not JSON) |
| `cookies()` | `{ name: value }`, strings |
| `body()` | Parsed body: a JSON object, or form fields as `{ name: value }` (repeated names as arrays) |
| `files(name)` | Uploaded files for a multipart field, as PocketBase `*filesystem.File` values ready for a record field |
| `ip` | Client IP as PocketBase resolves it (trusted proxy settings apply) |
| `event` | The underlying PocketBase `RequestEvent`, for anything not covered here |

Query values and cookies are strings unless you ask for JSON. Nothing is parsed for you.

## Examples

```ejs
<p>You asked for <%%= ctx.req.url.pathname %><%%= ctx.req.url.search %></p>
<p>Page <%%= Number(ctx.req.query('page') ?? 1) %></p>
<p>Browser: <%%= ctx.req.header('User-Agent') %></p>
```

### Forms

`body()` parses JSON, `application/x-www-form-urlencoded` and `multipart/form-data`:

```js
// pb_pages/contact/+page.js
module.exports = {
  post(ctx) {
    const { email, message } = ctx.req.body()
    // ...
  },
}
```

### File uploads

```html
<form method="post" enctype="multipart/form-data">
  <input type="file" name="avatar" />
  <button>Upload</button>
</form>
```

```js
module.exports = {
  post(ctx) {
    const record = ctx.app.findRecordById('users', ctx.auth.id)
    record.set('avatar', ctx.req.files('avatar')[0])
    ctx.app.save(record)
    return ctx.res.redirect('/account')
  },
}
```

### Cookies

```js
const theme = ctx.req.cookie('theme')                     // 'dark' or undefined
const prefs = ctx.req.cookie('prefs', { json: true }) ?? {}
```

Set cookies with [`ctx.res.cookie()`](/docs/ctx/res#cookies).
