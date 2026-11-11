---
title: ctx.env
description: Read environment variables.
---

# `ctx.env(name)`

Returns an environment variable, or `''` if it's unset.

```js
const key = ctx.env('STRIPE_SECRET_KEY')
```

It reads the app's environment. Under a multitenant host's sandbox, it reads the instance's environment.

See [Managing Secrets](/docs/secrets).
