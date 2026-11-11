# pocketpages.dev

The PocketPages website and docs, written as a PocketPages v1 app. It runs on PocketBase CE with the pages plugin
(`ext/pages`) and follows [`../SPEC.md`](../SPEC.md).

## Layout

- `pb_pages/` is the app: layouts, the splash page, and the docs under `(main)/docs/` (Markdown with EJS).
- `pb_pages/(main)/_private/toc.md` is the docs navigation.
- `package.json` only builds the CSS (Tailwind + daisyUI). The built `pb_pages/assets/app.css` is checked in, so
  you don't need Node to run the site.

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

## Rebuild the CSS

After changing classes in templates or `assets/app.tailwind.css`:

```bash
npm install
npm run build   # or: npm run dev, to watch
```
