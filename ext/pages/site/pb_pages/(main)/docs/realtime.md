---
title: Realtime Events
description: Send realtime messages from pages with the realtime plugin, and receive them in the browser with the PocketBase JS SDK.
---

# Realtime Events

PocketBase pushes realtime messages to browsers over server-sent events. In PocketPages, page code sends, and browsers receive.

## Sending from a page

Turn on the [`realtime` plugin](/docs/plugins/realtime) and call `ctx.realtime.send()`:

```js
// pb_pages/+config.js
module.exports = { plugins: ['realtime'] }
```

```js
// pb_pages/rooms/[id]/+api.js
module.exports = {
  post(ctx) {
    ctx.realtime.send(`rooms/${ctx.params.id}/delta`, ctx.req.body())
    return ctx.res.json({ ok: true })
  },
}
```

Limit recipients with a filter:

```js
ctx.realtime.send('alerts', 'Hi', { filter: (client) => !!client.get('auth') })
```

Page code can't subscribe to realtime topics itself; it only sends.

## Receiving in the browser

Use the [PocketBase JS SDK](https://github.com/pocketbase/js-sdk#realtimeservice):

```js
const pb = new PocketBase(location.origin)

const unsubscribe = await pb.realtime.subscribe(`rooms/${ROOM_ID}/delta`, (e) => {
  console.log('Received delta:', e)
})

// Later
unsubscribe()
```

The SDK reconnects on its own and resubscribes to its topics after a reconnect.

## Hypermedia

To push HTML instead of data, see [HTMX Integration](/docs/htmx#realtime-updates) and the [Datastar plugin](/docs/plugins/datastar#broadcasting).
