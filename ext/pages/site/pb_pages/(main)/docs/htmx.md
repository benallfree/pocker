---
title: 'HTMX Integration'
description: 'Use HTMX with PocketPages: route organization, HTML fragments without layouts, shared partials, forms, and realtime updates.'
---

# HTMX Integration with PocketPages

[HTMX](https://htmx.org) lets HTML elements fetch HTML from the server and swap it into the page. PocketPages renders that HTML.

## Setting up HTMX

Add HTMX to your layout:

```html
<script src="https://unpkg.com/htmx.org@1.9.10"></script>
```

## Route organization

HTMX requests want HTML fragments, not whole pages wrapped in your layout. Put the layout in a group, and the fragment routes outside it:

```
pb_pages/
  (site)/             # Pages with layouts
    +layout.ejs       # Main site layout
    index.ejs         # /
    products/
      index.ejs       # /products
  xapi/               # HTMX endpoints (no layouts)
    products/
      search.ejs      # /xapi/products/search
      create/
        +page.js
        index.ejs     # /xapi/products/create
```

1. `(site)` holds full pages. Its `+layout.ejs` wraps them, and the group adds nothing to the URL.
2. `xapi/` holds fragments. No layout applies to them, because there is no `+layout.ejs` at the root.

Don't name the folder `api/`: `/api/` belongs to PocketBase.

A handler can also send a fragment directly with `ctx.res.html()`, which never applies layouts.

## Sharing partials

Render the same fragment on the first page load and in HTMX responses by keeping it in a `_private/` partial:

```
pb_pages/
  _private/
    product-card.ejs    # shared partial
  (site)/
    products/
      index.ejs         # includes the partial
  xapi/
    products/
      more.ejs          # includes the same partial
```

```ejs
<!-- pb_pages/_private/product-card.ejs -->
<div class="product-card" id="product-<%%= product.id %>">
  <h3><%%= product.getString('name') %></h3>
  <p><%%= product.getString('description') %></p>
</div>
```

```ejs
<!-- pb_pages/(site)/products/index.ejs -->
<%% const products = ctx.app.findRecordsByFilter('products', 'active = true', 'name', 20) %>
<div class="products-container">
  <%% for (const product of products) { %>
    <%%- include('product-card', { product }) %>
  <%% } %>
</div>
<button hx-get="/xapi/products/more?page=2" hx-target=".products-container" hx-swap="beforeend">
  Load More
</button>
```

```ejs
<!-- pb_pages/xapi/products/more.ejs -->
<%%
  const page = Number(ctx.req.query('page') ?? 1)
  const products = ctx.app.findRecordsByFilter('products', 'active = true', 'name', 20, (page - 1) * 20)
%>
<%% for (const product of products) { %>
  <%%- include('product-card', { product }) %>
<%% } %>
```

## Search as you type

```html
<!-- pb_pages/(site)/index.ejs -->
<input
  type="search"
  name="q"
  hx-get="/xapi/products/search"
  hx-trigger="input changed delay:500ms"
  hx-target="#search-results"
  placeholder="Search products..."
/>
<div id="search-results"></div>
```

```ejs
<!-- pb_pages/xapi/products/search.ejs -->
<%%
  const products = ctx.app.findRecordsByFilter(
    'products', 'name ~ {:q}', 'name', 20, 0, { q: ctx.req.query('q') ?? '' }
  )
%>
<%% for (const product of products) { %>
  <%%- include('product-card', { product }) %>
<%% } %>
```

## Forms

```html
<!-- pb_pages/(site)/products/new.ejs -->
<form hx-post="/xapi/products/create" hx-target="#products-list" hx-swap="beforeend">
  <input type="text" name="name" placeholder="Product Name" required />
  <textarea name="description" placeholder="Description" required></textarea>
  <button type="submit">Create Product</button>
</form>
<div id="products-list"></div>
```

```js
// pb_pages/xapi/products/create/+page.js
module.exports = {
  post(ctx) {
    const { name, description } = ctx.req.body()
    const product = new Record(ctx.app.findCollectionByNameOrId('products'))
    product.set('name', name)
    product.set('description', description)
    ctx.app.save(product)
    return { product }
  },
}
```

```ejs
<!-- pb_pages/xapi/products/create/index.ejs -->
<%%- include('product-card', { product: data.product }) %>
```

## Out-of-band updates

Update more than one element with a single response:

```ejs
<!-- pb_pages/xapi/products/create/index.ejs -->
<%%- include('product-card', { product: data.product }) %>
<div id="product-count" hx-swap-oob="true"><%%= ctx.app.countRecords('products') %> Products</div>
```

## Polling

```html
<div hx-get="/xapi/products/latest" hx-trigger="every 30s">
  <!-- refreshed every 30 seconds -->
</div>
```

## Realtime updates

With the [`realtime` plugin](/docs/plugins/realtime), a page can push rendered HTML to every subscriber. A template has `include()`, so render the partial there and send it:

```js
// pb_pages/+config.js
module.exports = { plugins: ['realtime'] }
```

```js
// pb_pages/xapi/chat/+page.js
module.exports = {
  post() {}, // accept POST; the page below does the work
}
```

```ejs
<!-- pb_pages/xapi/chat/index.ejs -->
<%% ctx.realtime.send('chat', include('chat-msg', { message: ctx.req.body().message })) %>
```

```html
<!-- pb_pages/(site)/index.ejs -->
<form hx-post="/xapi/chat" hx-swap="none" hx-on::after-request="if(event.detail.successful) this.reset()">
  <input type="text" name="message" />
  <button type="submit">Send</button>
</form>
<div id="chat"></div>
```

In the browser, subscribe to the topic with the PocketBase JS SDK and append what arrives:

```html
<script src="https://unpkg.com/pocketbase/dist/pocketbase.umd.js"></script>
<script>
  const pb = new PocketBase(location.origin)
  pb.realtime.subscribe('chat', (html) => {
    document.getElementById('chat').insertAdjacentHTML('beforeend', html)
  })
</script>
```

## Best practices

1. **Route organization**: keep fragment routes outside the layout group, in a folder like `xapi/`.
2. **Response size**: return only the HTML the swap needs.
3. **Loading states**: use `hx-indicator` to show progress.
4. **Security**: check `ctx.auth` in middleware before changing data, validate input on the server, and debounce triggers like search inputs.
