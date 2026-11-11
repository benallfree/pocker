---
title: 'Route and Query Parameters'
description: 'Read route parameters from [param] file and folder names with ctx.params, and query strings with ctx.req.query().'
---

# Route and Query Parameters

Route parameters come from the URL path. Query parameters come from the query string. v1 keeps them apart.

## Route parameters

Use square brackets in file or folder names to capture a URL segment:

```
pb_pages/
  products/
    [productId]/
      index.ejs
      reviews/
        [reviewId].ejs
```

This handles URLs like `/products/123` and `/products/123/reviews/456`.

Read them through `params` (the same object as `ctx.params`):

```ejs
<!-- products/[productId]/reviews/[reviewId].ejs -->
<h1>Product: <%%= params.productId %></h1>
<p>Review: <%%= params.reviewId %></p>
```

Route parameters are always strings. `ctx.params` never includes query parameters.

## Query parameters

Read the query string with `ctx.req.query()`:

```
/products/123?sort=latest&tag=red&tag=blue
```

```ejs
<p>Sort by: <%%= ctx.req.query('sort') %></p>
<p>Tags: <%%= ctx.req.query('tag', { all: true }).join(', ') %></p>
```

| Call | Returns |
|---|---|
| `ctx.req.query(name)` | First value as a string, or `undefined` |
| `ctx.req.query(name, { all: true })` | Every value, as an array of strings |
| `ctx.req.query()` | `{ name: firstValue }` for every parameter |

Values are strings. Nothing is parsed for you; if you pass JSON in a query parameter, parse it yourself:

```js
const filters = JSON.parse(ctx.req.query('filters') ?? '{}')
```

## The route itself

`ctx.route` describes the matched route: `{ path, name, file }`. For `products/[productId]/index.ejs`, `path` is `/products/[productId]`, `name` is `index`, and `file` is `products/[productId]/index.ejs`.
