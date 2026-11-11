---
title: ctx.app
description: The PocketBase app - records, collections and settings.
---

# `ctx.app`

The app (`$app`): records, collections, settings. Page code calls it directly, with no HTTP round trip.

```js
module.exports = {
  load(ctx) {
    return {
      product: ctx.app.findRecordById('products', ctx.params.id),
      latest: ctx.app.findRecordsByFilter('posts', 'published = true', '-created', 10),
    }
  },
}
```

Everything in PocketBase's [JavaScript records](https://pocketbase.io/docs/js-records/) and [database](https://pocketbase.io/docs/js-database/) docs that uses `$app` works on `ctx.app`.

Calls through `ctx.app` don't apply collection API rules: page code is trusted server code. Check `ctx.auth` yourself before reading or changing data on a user's behalf.
