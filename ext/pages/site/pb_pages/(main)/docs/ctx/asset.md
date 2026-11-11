---
title: ctx.asset
description: Content-hashed URLs for static files in pb_pages.
---

# `ctx.asset(path)`

Returns the URL for a static file in `pb_pages`, with its content hash. In templates it's also available as `asset`.

```ejs
<link rel="stylesheet" href="<%%= asset('/assets/app.css') %>">
<!-- <link rel="stylesheet" href="/assets/app.3f9a1c2b7d4e.css"> -->
```

- Relative paths resolve from the current file's folder; `/`-paths from `pb_pages`.
- A path that isn't a file in `pb_pages` is returned unchanged.
- The hash is the first 12 hex characters of the file's SHA-256: `name.<hash>.ext`.
- Fingerprinted URLs are served with `Cache-Control: public, max-age=31536000, immutable`.

Relative image paths in Markdown pages go through `ctx.asset` automatically.

See [Asset Management](/docs/asset-management).
