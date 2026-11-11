# htmx starter

A realtime demo with [htmx](https://htmx.org/) and the built-in `realtime` plugin. The chat form posts to `/fragments/chat`, which renders the message with a private partial and broadcasts it on the `chat` topic; two connections on the page receive it over PocketBase realtime (`pocketbase-htmx-ext-sse`). The counter button swaps in the HTML fragment from `/fragments/count`. Fragment routes live in `pb_pages/fragments/`, outside the `(site)` group, so no layout wraps them (`/api/*` belongs to PocketBase).

```bash
pocketbase pages create myapp --starter htmx
cd myapp
pocketbase serve --dir ./pb_data
```

Then open http://127.0.0.1:8090.
