---
title: Errors
description: How PocketPages turns thrown errors into responses, ctx.httpError(), and a custom +error.ejs page.
---

# Errors

## Thrown errors

Anything thrown in middleware, a handler or a template becomes a `500`.

- **Dev mode or `debug: true`:** an HTML error page with the message, the file and line (template lines map to the `.ejs` or `.md` source), and the stack trimmed to `pb_pages`-relative paths.
- **Otherwise:** a plain "Something went wrong" page.

The full error always goes to the app's log.

## `ctx.httpError(status, message)`

Throw `ctx.httpError()` to answer with a specific status:

```js
// pb_pages/products/[id]/+page.js
module.exports = {
  load(ctx) {
    const product = ctx.app.findFirstRecordByData('products', 'slug', ctx.params.id)
    if (!product) throw ctx.httpError(404, 'No such product')
    ctx.data.product = product
  },
}
```

- In a page, it sends that status with a plain page showing `message`.
- In a `+api.js`, it sends JSON: `{ "status": 404, "message": "No such product" }`.

## A custom error page

Create `pb_pages/+error.ejs`. It renders for every `ctx.httpError` and for every `500` outside dev mode, inside the root layout, with `ctx.error = { status, message }`:

```ejs
<!-- pb_pages/+error.ejs -->
<%% slot('title', () => { %>Error <%%= ctx.error.status %><%% }) %>
<h1><%%= ctx.error.status %></h1>
<p><%%= ctx.error.message %></p>
```
