---
title: Working with Private Files and Directories in PocketPages
description: How _private folders hold partials and JS modules for include() and ctx.resolve(), and which names are never routed.
---

# Working with Private Files and Directories

## Names that are never routed

| Prefix | Meaning |
|---|---|
| `+` | Framework file (`+layout.ejs`, `+page.js`, ...) |
| `_` | Private. `_private/` folders hold partials and modules. Other `_` names are ignored. |
| `-` | Ignored (drafts, notes) |
| `.` | Ignored (dotfiles) |

```
pb_pages/
  _private/        # private (not routable)
  -drafts/         # ignored
  helpers/         # normal folder (routable)
  +page.js         # framework file (not routable)
  +layout.ejs      # framework file (not routable)
  index.ejs        # page
```

## The `_private` folder

`_private/` folders serve two purposes:

1. Template partials, rendered with [`include()`](/docs/partials)
2. JavaScript modules and other files, loaded with [`ctx.resolve()`](/docs/ctx/resolve)

Both use the same lookup: `_private/` in the current template's folder, then each parent folder up to `pb_pages/`. A name starting with `/` looks only in `pb_pages/_private/`.

```
pb_pages/
  _private/
    header.ejs
    format.js
  products/
    _private/
      product-card.ejs
      queries.js
    index.ejs
```

```ejs
<!-- In products/index.ejs -->
<%%
  const queries = ctx.resolve('queries')   // products/_private/queries.js
  const format = ctx.resolve('format')     // _private/format.js
%>
<%%- include('header') %>                  <!-- _private/header.ejs -->
<%% for (const p of queries.featured(ctx.app)) { %>
  <%%- include('product-card', { p, format }) %>
<%% } %>
```

## Modules

A module in `_private/` is a CommonJS file:

```js
// pb_pages/products/_private/queries.js
module.exports = {
  featured: (app) => app.findRecordsByFilter('products', 'featured = true', '-created', 10),
}
```

## Notes

1. Files in `_private/` are never served.
2. Keep secrets in environment variables (see [Managing Secrets](/docs/secrets)), not in files.
3. Keep private files close to where they're used, and hoist them up when several sections share them.
