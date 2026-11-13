---
title: Going Live
description: Deploy a PocketPages app by putting pb_pages next to pb_data on a PocketOne server with the pages plugin.
---

# Going Live

A PocketPages app is a `pb_pages` folder. To go live, run PocketOne with the pages plugin on your server and put `pb_pages` next to `pb_data`, along with your other `pb_*` folders:

```
pb_data/
pb_hooks/
pb_migrations/
pb_public/
pb_pages/
```

Or point PocketBase at it with `--pagesDir`.

## Updating

Copy the changed files into `pb_pages`. The route table is rebuilt on any change, and templates are cached by content hash, so updates show on the next request without restarting the app. If an update has a startup error (a bad name or a route conflict), it's logged with the file path and the previous route table keeps serving.

## Before you go live

- Run without `--dev`, and keep `debug: false` in `+config.js`.
- Set secrets as environment variables (see [Managing Secrets](/docs/secrets)).
- Serve over HTTPS. Cookies set with `ctx.res.cookie` are marked `Secure` on HTTPS requests.

## Multitenant hosts

Under a multitenant PocketOne host, every instance has its own `pb_pages`, and page code runs with the same limits as the rest of the instance's JS.
