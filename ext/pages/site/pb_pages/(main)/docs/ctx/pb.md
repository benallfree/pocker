---
title: ctx.pb() (JS SDK)
description: Query your app with the PocketBase JS SDK from page code, as the signed-in user.
---

# `ctx.pb()`: the JS SDK in page code

`ctx.pb()` returns a [PocketBase JS SDK](https://github.com/pocketbase/js-sdk) client, the same API you use in the
browser:

```js
// pb_pages/posts/+page.js
module.exports = {
  load(ctx) {
    const page = ctx.pb().collection('posts').getList(1, 20, {
      filter: 'published = true',
      sort: '-created',
      expand: 'author',
    })
    return { posts: page.items }
  },
}
```

- **It acts as the visitor.** When someone is signed in (`ctx.auth`, for example through the `auth` plugin), the
  client is authenticated as them; otherwise it's a guest. Your collections' API rules, `expand` and hidden fields
  apply exactly as they would in the browser.
- **It's synchronous.** Calls return their result directly; no `await`.
- **No network.** Requests go straight to your app's own API inside PocketBase. There's no host or port to set, and
  it works the same on any server.
- **Errors** throw the SDK's `ClientResponseError`:

  ```js
  try {
    const post = ctx.pb().collection('posts').getOne(ctx.params.id)
  } catch (e) {
    if (e.status === 404) throw ctx.httpError(404, 'No such post')
    throw e
  }
  ```

- **Limits:** only `/api/` routes; file uploads through the SDK aren't supported, so use `ctx.app` for those.

## `ctx.pb()` or `ctx.app`?

| | `ctx.pb()` | `ctx.app` |
|---|---|---|
| Permissions | the visitor's: API rules apply | full access, no rules |
| API | the JS SDK, same as the browser | PocketBase's JSVM API (`findRecordsByFilter`, `save`, ...) |
| Use it for | reading and writing on the visitor's behalf | admin work, background jobs, anything rules shouldn't limit |

When in doubt, use `ctx.pb()`: if a page shows something, the visitor is allowed to see it.
