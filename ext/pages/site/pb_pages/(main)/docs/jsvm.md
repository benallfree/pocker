---
title: JavaScript Runtime
description: Where page code runs, what it can reach, and using npm packages from pb_pages/node_modules.
---

# JavaScript Runtime

Middleware, handlers, templates and plugins run in PocketBase's JavaScript runtime. It is neither a browser nor Node.js, so there are no Web APIs or Node APIs such as `Buffer` and `require('fs')`.

Each request runs as one unit under the app's usual JS limits (execution time, memory). Under a multitenant host, that's the instance's sandbox.

## Talking to PocketBase

Use [`ctx.app`](/docs/ctx/app), which is `$app`. PocketBase's [JS docs](https://pocketbase.io/docs/js-overview/) cover [database operations](https://pocketbase.io/docs/js-database/) and [record operations](https://pocketbase.io/docs/js-records/):

```js
const records = ctx.app.findRecordsByFilter(
  'articles',
  'status = {:status} && author = {:author}',
  '-created',
  20,
  0,
  { status: 'pending', author: ctx.auth.id }
)
```

## Can I use my favorite npm package?

Maybe. Page code can `require()` modules from `pb_pages/node_modules`, then from the app's usual module paths. Packages that are plain CommonJS JavaScript, with no Node or browser APIs, have the best chance of working.

```bash
cd pb_pages
npm install some-package
```

```js
const somePackage = require('some-package')
```

Your own modules go in `_private/` and load with [`ctx.resolve()`](/docs/ctx/resolve).
