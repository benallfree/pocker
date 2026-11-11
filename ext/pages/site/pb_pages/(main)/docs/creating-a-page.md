---
title: Creating a PocketPages Page
description: Create .ejs and .md files in pb_pages to add pages, with index files and nested folders mapping to URLs.
---

# Creating a PocketPages Page

A **page** is a template file, `.ejs` or `.md`, anywhere in `pb_pages/`. Its path becomes its URL.

## Creating pages

1. **Create a file**: inside `pb_pages/`, create an `.ejs` file. For example, an "About" page:

```text
pb_pages/about.ejs  -> serves "/about"
```

2. **`index` files**: `index` maps to its folder.

```text
pb_pages/index.ejs           -> serves "/"
pb_pages/products/index.ejs  -> serves "/products" (and "/products/")
```

3. **Nested pages**: folders become URL segments.

```text
pb_pages/
├── index.ejs         -> "/"
├── about.ejs         -> "/about"
├── contact.ejs       -> "/contact"
└── products/
    ├── index.ejs     -> "/products"
    └── details.ejs   -> "/products/details"
```

4. **Markdown pages**: a `.md` file works the same way. It runs through EJS first, then Markdown. See [Markdown](/docs/markdown).

```text
pb_pages/docs/routing.md  -> "/docs/routing"
```

The extension is always dropped from the URL. To add data or handle forms, put a `+page.js` next to the page; see [Loading Data](/docs/loading-data).
