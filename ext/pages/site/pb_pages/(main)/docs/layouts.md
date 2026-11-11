---
title: Layouts and Slots
description: Wrap pages with +layout.ejs files, and pass content between pages and layouts with named slots.
---

# Layouts and Slots

`+layout.ejs` files wrap pages. A page's layouts are every `+layout.ejs` from its own folder up to `pb_pages/`, applied innermost first. A folder may have one layout.

```plaintext
pb_pages/
  +layout.ejs          # applied last (outermost)
  products/
    +layout.ejs        # applied first
    [id].ejs
```

For `/products/123`, `[id].ejs` renders, then `products/+layout.ejs` wraps it, then `+layout.ejs` wraps that.

## `slot()`

In a layout, `slot()` returns the content being wrapped: the page, or the inner layout.

```ejs
<!-- pb_pages/+layout.ejs -->
<!DOCTYPE html>
<html lang="en">
<head>
  <title>My Application</title>
</head>
<body>
  <main>
    <%%- slot() %>
  </main>
  <footer>&copy; <%%= new Date().getFullYear() %> My Application</footer>
</body>
</html>
```

## Named slots

A page (or an inner layout) defines a named slot with `slot(name, fn)`. `fn`'s output is captured and nothing is output in place. A layout reads it with `slot(name)`, which returns `''` if it isn't defined.

```ejs
<!-- pb_pages/products/[id].ejs -->
<%% slot('title', () => { %><%%= data.product.getString('name') %><%% }) %>
<h1><%%= data.product.getString('name') %></h1>
```

```ejs
<!-- pb_pages/+layout.ejs -->
<html>
  <head><title><%%- slot('title') || 'My shop' %></title></head>
  <body><%%- slot() %></body>
</html>
```

- Named slots defined by the page or by any inner layout are visible to every outer layout.
- Defining the same name again replaces it. Inside the new definition's `fn`, `slot(name)` still returns the previous content, so a layout can wrap it:

```ejs
<!-- pb_pages/docs/+layout.ejs -->
<%% slot('title', () => { %>Docs: <%%- slot('title') %><%% }) %>
<main><%%- slot() %></main>
```

## Data in layouts

Layouts render after the page, with the same `ctx`. Everything middleware, `+page.js` and the page put on `ctx.data` or `ctx.meta` is there.

## When layouts are skipped

- When the response isn't HTML (a handler set another `Content-Type`).
- When a handler or middleware sent the response itself (`ctx.res.json`, `ctx.res.html`, `ctx.res.redirect`, ...).
- For `+api.js` routes, which have no template.
- With the [datastar plugin](/docs/plugins/datastar), for requests that carry `Datastar-Request: true`.

To serve HTML fragments without the site layout, put the layout in a group (`(site)/+layout.ejs`) and the fragments outside it, or send them with `ctx.res.html()`.
