# DaisyUI docs starter

A docs site built from Markdown pages, styled with [Tailwind CSS](https://tailwindcss.com/) and [DaisyUI](https://daisyui.com/). It shows layout groups (`(splash)` for the home page, `(main)` for the docs), nested layouts, and a Markdown partial (`docs/_private/toc.md`) as the table of contents. Add a page by dropping a `.md` file in `pb_pages/(main)/docs/`. The compiled `pb_pages/assets/app.css` is included; npm is only for rebuilding the CSS.

```bash
pocketbase pages create myapp --starter daisyui-docs
cd myapp
pocketbase serve --dir ./pb_data
```

Then open http://127.0.0.1:8090.

To change styles, rebuild the CSS while you edit:

```bash
npm i
npm run dev:css
```
