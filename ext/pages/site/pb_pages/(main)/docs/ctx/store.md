---
title: ctx.store
description: The app's in-memory store, shared across requests.
---

# `ctx.store(name, value?)`

The app's in-memory store (`$app.store()`). Set with two arguments, read with one.

```js
ctx.store('visits', (ctx.store('visits') || 0) + 1)
```

```ejs
<p>Visits since start: <%%= ctx.store('visits') %></p>
```

- Values are shared by every request to the app.
- The store lives in memory: it's empty again after the app restarts. Keep anything that must last in a collection.
