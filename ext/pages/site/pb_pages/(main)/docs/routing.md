---
title: 'File-Based Routing'
description: 'How files in pb_pages map to URLs: pages, [param] segments, groups, API routes, static files, and which route wins.'
---

# File-Based Routing

URLs come from the files and folders in `pb_pages/`.

## Pages

| File | URL |
|---|---|
| `index.ejs` | `/` |
| `about.ejs` | `/about` |
| `docs/index.md` | `/docs` (and `/docs/`) |
| `docs/routing.md` | `/docs/routing` |
| `products/[id].ejs` | `/products/123`, with `ctx.params.id = "123"` |
| `[org]/[repo]/index.ejs` | `/acme/web`, with `ctx.params = { org: "acme", repo: "web" }` |

- The extension is dropped. `index` maps to its folder.
- `[name]` matches exactly one non-empty segment. A parameter must be the whole segment: `post-[id].ejs` is an error at startup.
- Segment counts must match: `products/[id].ejs` doesn't match `/products/123/reviews`.
- A URL with a trailing slash matches the same route as without it. Pages never redirect to add or remove one.
- Matching is case-sensitive.

See [Accessing Parameters](/docs/parameters).

## Groups

A folder named `(name)` doesn't add a URL segment. Use groups to give part of the site its own layout or middleware:

```text
pb_pages/
  (site)/
    +layout.ejs     # wraps everything in (site)
    index.ejs       # /
    about.ejs       # /about
  xapi/
    count.ejs       # /xapi/count, outside the (site) layout
```

## API routes

A `+api.js` file serves its folder's URL without a template: `feeds/rss/+api.js` serves `/feeds/rss`. `[param]` folders work as for pages. A folder can't have both a `+api.js` and an `index` page. See [API Routes and JSON](/docs/api-routes).

## Static files

Every other file is served as-is at its path, with groups removed: `assets/app.css` serves `/assets/app.css`. See [Static content](/docs/static-content).

## Which route wins

1. `/api/...` and `/_/...` belong to PocketBase. Pages never see them.
2. Among matching routes the most specific wins: segments are compared from the left, and a literal segment beats a `[param]`. So `products/new.ejs` beats `products/[id].ejs` for `/products/new`.
3. A static file and a page can't claim the same URL (startup error).
4. A URL nothing in `pb_pages` matches goes on to `pb_public`, then to PocketBase.

## When routes change

The route table is built when the app starts and rebuilt whenever anything in `pb_pages` changes. A rebuild never restarts the app or reloads `pb_hooks`. Startup errors (bad names, conflicts) are logged with the file path, and the previous route table keeps serving.
