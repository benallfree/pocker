---
title: PocketPages is now built into PocketOne
description: PocketPages v1 ships inside PocketOne. No npm install, no boot file, JSX and TypeScript pages, and 11 to 17 times the requests per second.
date: 2026-11-11
author: Ben Allfree
tags: release, v1, PocketOne
---

PocketPages used to be an npm package you installed next to PocketBase and booted from a hook file. As of today it's
part of [PocketOne](https://github.com/pockethost/pocketone), a community fork of PocketBase that
PocketHost maintains. Put a `pb_pages` folder next to `pb_data` and it's served.

```bash
brew install pockethost/tap/pocketone
pocketbase pages create myapp --starter daisyui-docs
cd myapp
pocketbase serve --dir ./pb_data
```

## What's new in v1

- **Nothing to install.** No `node_modules`, no `pocketpages.pb.js`, no plugin packages. Routing, static files and
  templates run in Go; your page code runs in PocketBase's JS VM, as before.
- **JSX and TSX pages.** Write `.tsx` pages, layouts and components next to your `.ejs` ones. esbuild is built in,
  so there's still no build step. `+page.ts` and the rest of your page code can be TypeScript too.
- **A cleaner `ctx` API.** One `ctx` object for requests, responses, data, sessions and helpers. `ctx.pb()` gives you
  the PocketBase JS SDK in-process, as the visitor, so API rules apply.
- **Auth built in.** Password, OTP, OAuth2, anonymous sign-in and the email flows, with cookie sessions.
- **Faster.** On the same starter kit pages, v1 serves about 17 times the requests per second of the npm package on
  a simple page and 11 times on a docs page, and a single request takes 0.2 to 0.4 ms instead of 6 ms.

This site runs on v1 now, on a PocketHost edge in Los Angeles.

## Coming from 0.x

v1 keeps the ideas (a folder of files that become routes, EJS, layouts, partials, middleware) and changes the API.
[Moving from PocketPages 0.x](/docs/moving-from-0x) lists every difference.

The npm package is retiring: v1 is where new work happens. Apps on 0.x keep running where they run today.
