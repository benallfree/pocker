---
title: Starter Kits
description: Start a new PocketPages app from a starter kit with pocketbase pages create.
---

# Starter Kits

To start a new app from a starter kit, run:

```bash
pocketbase pages create myapp --starter <kit>
```

Without `--starter`, `pocketbase pages create myapp` creates a plain app (the `minimal` kit). `pocketbase pages starters`
lists the kits. The folder must not exist yet. Then run it:

```bash
cd myapp
pocketbase serve --dir ./pb_data
```

PocketBase keeps `pb_data` next to its executable unless you pass `--dir`; `pb_pages` and `pb_migrations` are found next
to `pb_data`.

Each starter kit is an ordinary `pb_pages` folder written against the v1 API, so everything in these docs applies to it.
