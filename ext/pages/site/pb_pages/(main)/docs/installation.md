---
title: Installation
description: Run PocketPages v1 on PocketOne with the pages plugin, using a pb_pages folder.
---

# Installation

PocketPages v1 is built into PocketOne as the `pages` plugin (`ext/pages`). There is nothing to install from npm. You need a PocketOne binary with the plugin.

<%- include('quickstart') %>

`pocketbase pages create myapp` creates a new app. Add `--starter <kit>` to start from a [starter kit](/docs/starter-kits).

_Note: `--dir` tells PocketBase where `pb_data` is. PocketPages looks for `pb_pages` next to it._

Browse to `http://localhost:8090` and you'll see your home page:

<%- include('browser', { content: `Hello, world!` }) %>

To start editing, open

```
myapp/pb_pages/index.ejs
```

Changes appear on the next refresh. The app doesn't restart.

## Where pages live

Pages live in their own folder next to PocketBase's others:

```
myapp/
  pb_data/  pb_hooks/  pb_migrations/  pb_public/
  pb_pages/
```

- PocketPages finds the folder at `pb_pages` next to `pb_data`, or wherever `--pagesDir` points.
- If the folder doesn't exist, the plugin does nothing for that app.
- Under a multitenant host, every instance has its own `pb_pages`.

## How does it work?

PocketPages is a multi-page app server for PocketBase. It listens for requests and renders pages using file-based routing rules, like old-school PHP. URLs under `/api/` and `/_/` always belong to PocketBase. A URL nothing in `pb_pages` matches goes on to `pb_public`, then to PocketBase.
