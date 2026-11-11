---
title: Directory Structure
description: How a pb_pages folder is organized, and the special +, _, - and . prefixes.
---

# Directory Structure

All pages live in the `pb_pages/` folder, next to `pb_data/`:

```
myapp/
  pb_data/
  pb_hooks/
  pb_migrations/
  pb_public/
  pb_pages/
    +config.js
    +layout.ejs
    index.ejs
    about.ejs
    _private/
      nav.ejs
    assets/
      app.css
    products/
      [id]/
        +page.js
        index.ejs
```

## Special names

A file or folder name starting with one of these characters is never a URL:

| Prefix | Meaning |
|---|---|
| `+` | Framework file: `+config.js`, `+layout.ejs`, `+middleware.js`, `+page.js`, `+api.js`. Any other `+` name is an error at startup. |
| `_` | Private. `_private/` folders hold partials and modules for `include()` and `ctx.resolve()`. Other `_` names are ignored. |
| `-` | Ignored (drafts, notes). |
| `.` | Ignored (dotfiles). |

`node_modules/` is never routed either.

## Groups

A folder named `(name)` groups files without adding a URL segment: `(marketing)/pricing.ejs` serves `/pricing`. A group can have its own `+layout.ejs`, `+middleware.js` and `_private/`.

## What else

- Every other file is a [static file](/docs/static-content), served as-is.
- Page code can `require()` modules from `pb_pages/node_modules`, then from the app's usual module paths.
- Pages' own file access (routes, static files, includes, assets) never leaves `pb_pages`.
- A `pb_pages/api/` folder is an error at startup, because `/api/` belongs to PocketBase.
