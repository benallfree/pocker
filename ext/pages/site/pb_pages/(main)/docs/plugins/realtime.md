---
title: Realtime Plugin
description: The built-in realtime plugin sends messages to PocketBase realtime subscribers with ctx.realtime.send().
---

# Realtime Plugin

The `realtime` plugin sends messages to [PocketBase realtime](https://pocketbase.io/docs/realtime/) subscribers.

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['realtime'],
}
```

## `ctx.realtime.send(topic, data, { filter }?)`

Sends `data` to the subscribers of `topic`. `filter(client)` may limit the recipients.

```js
// pb_pages/ping/+api.js
module.exports = {
  post(ctx) {
    ctx.realtime.send('ping', { at: new Date().toISOString() })
    return ctx.res.json({ ok: true })
  },
}
```

On the client, subscribe with the PocketBase JS SDK:

```html
<script src="https://unpkg.com/pocketbase/dist/pocketbase.umd.js"></script>
<script>
  const pb = new PocketBase(location.origin)
  pb.realtime.subscribe('ping', (data) => console.log('Received:', data))
</script>
```

## Filtering recipients

```js
ctx.realtime.send('alerts', 'Maintenance at 10:00', {
  filter: (client) => !!client.get('auth'),
})
```

## Sending HTML

For hypermedia libraries (HTMX, Datastar), send rendered HTML. `include()` is available in templates, so render the partial there:

```js
// pb_pages/xapi/chat/+page.js
module.exports = {
  post() {}, // accept POST; the page renders and sends
}
```

```ejs
<!-- pb_pages/xapi/chat/index.ejs -->
<%% ctx.realtime.send('chat', include('chat-msg', { message: ctx.req.body().message })) %>
```

See [Realtime Events](/docs/realtime) and the [Datastar plugin](/docs/plugins/datastar), which can send Datastar patches over realtime.
