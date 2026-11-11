---
title: ctx.flash
description: One-time messages carried across a redirect in a cookie.
---

# `ctx.flash()`

A flash is a one-time message that survives one redirect, such as "Saved" after a form post.

## Setting a flash

```js
return ctx.res.redirect('/account', { flash: 'Saved' })
```

The message is stored as JSON in a cookie (`pp_flash`, `HttpOnly`, `Max-Age=300`). It can be any JSON value:

```js
return ctx.res.redirect('/cart', { flash: { type: 'success', text: 'Added to cart' } })
```

## Reading it

On the next request, `ctx.flash()` returns the message and clears the cookie. It returns `undefined` when there is none. Calling it again in the same request returns the same value.

```ejs
<%% const flash = ctx.flash() %>
<%% if (flash) { %>
  <div class="alert alert-<%%= flash.type %>"><%%= flash.text %></div>
<%% } %>
```

Read it in your layout to show flashes on every page.
