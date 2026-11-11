---
title: Understanding Caching in PocketPages
description: How PocketPages caches templates, static files and fingerprinted assets.
---

# Understanding Caching in PocketPages

## Fingerprinted assets

`asset()` adds a content hash to a file's URL:

```ejs
<img src="<%%= asset('/images/logo.png') %>">
<!-- <img src="/images/logo.3f9a1c2b7d4e.png"> -->
```

- The hash is the first 12 hex characters of the file's SHA-256.
- Fingerprinted URLs are served with `Cache-Control: public, max-age=31536000, immutable`, so browsers and CDNs keep them for a year.
- When the file changes, the hash changes, and so does the URL. Old copies are never served for the new URL.
- A path that isn't a file in `pb_pages` is returned unchanged (and not fingerprinted).

## Static files

Every static file gets an `ETag` from its content hash. A request with a matching `If-None-Match` gets `304 Not Modified`.

## Templates

A template is compiled once per content hash and reused. Edit a file and the next request uses the new version, in every mode.

## Routes

The route table is built at startup and rebuilt whenever anything in `pb_pages` changes. A rebuild never restarts the app.

## Pages

Rendered pages aren't cached. To let browsers or a CDN cache a page, set a header in a handler or middleware:

```js
module.exports = (ctx, next) => {
  ctx.res.header('Cache-Control', 'public, max-age=60')
  return next()
}
```
