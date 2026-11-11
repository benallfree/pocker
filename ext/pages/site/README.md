# pocketpages.dev

The PocketPages website and docs, written as a PocketPages v1 app. It runs on PocketBase CE with the pages plugin
(`ext/pages`) and follows [`../SPEC.md`](../SPEC.md).

## Layout

- `pb_pages/` is the app: layouts, the splash page, and the docs under `(main)/docs/` (Markdown with EJS).
- `pb_pages/(main)/_private/toc.md` is the docs navigation.
- `pb_pages/(main)/blog/` is the blog: every `.md` file there is a post (front matter: title, description, date,
  author, tags).
- `pb_pages/assets/site.css` is the whole stylesheet: hand-written, no build step (the "Paper" theme).

## Run it

With a PocketBase CE binary that includes `ext/pages`, from this folder:

```bash
pocketbase serve --dev --dir ./pb_data
```

PocketPages finds `pb_pages` next to `pb_data`. Or point at it from anywhere:

```bash
pocketbase serve --dev --dir /path/to/pb_data --pagesDir /path/to/site/pb_pages
```

Open http://localhost:8090.
