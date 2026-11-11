---
title: Asset Management
description: Static files in pb_pages get content-hashed URLs through asset() for long-term caching.
---

# Asset Management

Static files in `pb_pages` (images, CSS, JS, fonts) can be served with fingerprinted URLs that change when the file changes.

## Where to put assets

Anywhere in `pb_pages`, next to the pages that use them or in a shared folder:

```
pb_pages/
  assets/
    app.css
    logo.png
  feature/
    index.md
    header.jpg
```

## In templates

Use `asset()` (the same as `ctx.asset()`):

```ejs
<link rel="stylesheet" href="<%%= asset('/assets/app.css') %>">
<img src="<%%= asset('header.jpg') %>">
```

`asset('/assets/app.css')` returns something like `/assets/app.3f9a1c2b7d4e.css`.

- Relative paths resolve from the current file's folder; `/`-paths from `pb_pages`.
- A path that isn't a file in `pb_pages` is returned unchanged.

## In Markdown

Relative image paths go through `asset()` automatically:

```markdown
![Header](header.jpg)
```

## How it works

1. A fingerprinted name carries the first 12 hex characters of the file's SHA-256: `name.<hash>.ext`.
2. Fingerprinted URLs are served with `Cache-Control: public, max-age=31536000, immutable`.
3. When the file changes, its hash and URL change, so browsers and CDNs fetch the new one.

See [asset](/docs/ctx/asset) and [Caching](/docs/caching).
