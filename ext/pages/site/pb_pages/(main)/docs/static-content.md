---
title: Serving Static Content in PocketPages
description: Files in pb_pages that aren't pages or framework files are served as-is, with content types and ETags.
---

# Serving Static Content in PocketPages

Any file in `pb_pages` that isn't a page (`.ejs`, `.md`), a framework file (`+...`), private (`_...`) or ignored (names starting with `-` or `.`) is a static file. It's served as-is at its path, with groups removed:

```
pb_pages/
  assets/
    app.css            -> /assets/app.css
    logo.png           -> /assets/logo.png
  documents/
    terms.pdf          -> /documents/terms.pdf
  (marketing)/
    brochure.pdf       -> /brochure.pdf
  favicon.ico          -> /favicon.ico
  index.ejs            -> /  (a page, not static)
```

- The content type comes from the extension.
- The `ETag` comes from the content hash; a matching `If-None-Match` gets `304`.
- A static file and a page can't claim the same URL (startup error).

For cache-friendly URLs, reference files through [`asset()`](/docs/asset-management).

## `pb_pages` and `pb_public`

A URL nothing in `pb_pages` matches goes on to `pb_public`, so you can keep using `pb_public` for files you don't want next to your pages.
