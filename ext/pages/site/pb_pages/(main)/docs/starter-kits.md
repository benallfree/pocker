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

| Starter | What it shows | Live demo |
| --- | --- | --- |
| **minimal** | A minimal PocketPages starter with just the basics | [pocketpages-minimal.pockethost.io](https://pocketpages-minimal.pockethost.io) |
| **mvp** | MVP.css, a minimalist classless CSS framework | [pocketpages-mvp.pockethost.io](https://pocketpages-mvp.pockethost.io) |
| **auth** | Password, anonymous, OTP and OAuth2 sign-in, plus verification, password reset and email change | [pocketpages-auth.pockethost.io](https://pocketpages-auth.pockethost.io) |
| **daisyui** | Tailwind CSS with DaisyUI components | [pocketpages-daisyui.pockethost.io](https://pocketpages-daisyui.pockethost.io) |
| **daisyui-docs** | Documentation site template with DaisyUI and navigation | [pocketpages-daisyui-docs.pockethost.io](https://pocketpages-daisyui-docs.pockethost.io) |
| **htmx** | HTMX integration for dynamic interactions | [pocketpages-htmx.pockethost.io](https://pocketpages-htmx.pockethost.io) |
| **datastar** | Realtime chat and counter using Datastar | [pocketpages-datastar.pockethost.io](https://pocketpages-datastar.pockethost.io) |
