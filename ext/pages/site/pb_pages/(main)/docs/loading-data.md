---
title: Loading Data in PocketPages
description: Load data inline in templates or in a +page.js file with load and per-method handlers.
---

# Loading Data in PocketPages

You can load data right in a template, or in a `+page.js` file next to it.

## Inline data loading

The simplest way is a `<%% %>` block at the top of your template:

```ejs
<%%
  const products = ctx.app.findRecordsByFilter('products', 'active = true', 'name', 100)
%>

<h1>Products</h1>
<%% for (const product of products) { %>
  <div class="product">
    <h2><%%= product.getString('name') %></h2>
  </div>
<%% } %>
```

## Server script blocks

For better formatting support in editors, you can write `<script server<%- '>' %>` instead of `<%% %>`. They mean the same thing:

```ejs
<script server<%- '>' %>
  const products = ctx.app.findRecordsByFilter('products', 'active = true', 'name', 100)
  const categories = ctx.app.findAllRecords('categories')
</script>

<h1>Products by Category</h1>
<%% for (const category of categories) { %>
  <h2><%%= category.getString('name') %></h2>
<%% } %>
```

## Structured loading with `+page.js`

A `+page.js` file keeps data loading and form handling out of the template:

```js
// pb_pages/products/[id]/+page.js
module.exports = {
  load(ctx) {
    ctx.data.product = ctx.app.findRecordById('products', ctx.params.id)
  },
  post(ctx) {
    const { qty } = ctx.req.body()
    // ...
    return ctx.res.redirect(`/products/${ctx.params.id}`, { flash: 'Added to cart' })
  },
}
```

```ejs
<!-- pb_pages/products/[id]/index.ejs -->
<h1><%%= data.product.getString('name') %></h1>
<%% const flash = ctx.flash() %>
<%% if (flash) { %><p><%%= flash %></p><%% } %>
```

### Exports

`+page.js` exports any of `load`, `get`, `post`, `put`, `patch` and `delete`.

1. `load` runs first, for every method.
2. Then the handler for the request method runs, if there is one.
3. Then the page renders.

A handler can set fields on `ctx.data`, or return a plain object. A returned object is shallow-assigned into `ctx.data` (`Object.assign`); there is no deep merge.

```js
module.exports = {
  load(ctx) {
    return { departments: ctx.app.findAllRecords('departments') }
  },
  get(ctx) {
    ctx.data.mode = 'view'
  },
}
```

If a handler sends a response (`ctx.res.redirect`, `json`, `html`, `text`, `file`, `sse`), the page doesn't render.

### One `+page.js` per folder

A `+page.js` applies to every page in its folder. Use `ctx.route.name` to tell them apart:

```js
module.exports = {
  load(ctx) {
    if (ctx.route.name === 'details') {
      ctx.data.reviews = ctx.app.findRecordsByFilter('reviews', 'product = {:id}', '-created', 20, 0, { id: ctx.params.id })
    }
  },
}
```

### Methods without a handler

- `GET` and `HEAD` still render the page.
- Other methods answer `405`, with an `Allow` header listing `GET, HEAD` and the methods that have handlers.

## Example: a contact form

```js
// pb_pages/contact/+page.js
module.exports = {
  load(ctx) {
    return { departments: ctx.app.findAllRecords('departments') }
  },
  post(ctx) {
    const { email, message, department } = ctx.req.body()
    try {
      const record = new Record(ctx.app.findCollectionByNameOrId('messages'))
      record.set('email', email)
      record.set('message', message)
      record.set('department', department)
      ctx.app.save(record)
    } catch (e) {
      return { error: 'Failed to send message', values: { email, message, department } }
    }
    return ctx.res.redirect('/contact', { flash: 'Thanks, we got your message.' })
  },
}
```

```ejs
<!-- pb_pages/contact/index.ejs -->
<%% const flash = ctx.flash() %>
<%% if (flash) { %><p><%%= flash %></p><%% } %>
<%% if (data.error) { %><mark><%%= data.error %></mark><%% } %>

<form method="post">
  <input name="email" type="email" value="<%%= data.values?.email %>" />
  <select name="department">
    <%% for (const d of data.departments) { %>
      <option value="<%%= d.id %>"><%%= d.getString('name') %></option>
    <%% } %>
  </select>
  <textarea name="message"><%%= data.values?.message %></textarea>
  <button type="submit">Send</button>
</form>
```

## Middleware or `+page.js`?

- Only the route's own folder's `+page.js` runs.
- Every `+middleware.js` from `pb_pages/` down to the route's folder runs, root first.

Use [middleware](/docs/middleware) for data or checks that a whole section of the site shares.
