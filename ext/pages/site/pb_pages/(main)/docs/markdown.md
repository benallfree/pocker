---
title: Markdown Support
description: .md pages and partials run through EJS, then Markdown, with YAML front matter passed to meta().
---

# Markdown Support

A `.md` file is a page, just like an `.ejs` file. Markdown is always on.

## How it works

1. **EJS first**: the file runs through EJS, so every [EJS tag](/docs/ejs) works in Markdown.
2. **Then Markdown**: the result is rendered as Markdown: CommonMark plus GFM tables, strikethrough, task lists and autolinks.

## Front matter

YAML between `---` lines at the top of the file is front matter. Each top-level key is passed to `ctx.meta(key, value)`:

```markdown
---
title: My Blog Post
description: A detailed guide about something interesting
---

# Content starts here...
```

Read the values in a layout with `meta`:

```ejs
<title><%%= meta('title') %></title>
<meta name="description" content="<%%= meta('description') %>">
```

## Headings

Headings get `id`s, so you can link to them: the text is lowercased, characters other than `a-z`, `0-9`, `-`, `_` and space are removed, and spaces become `-`. `## Front matter` gets `id="front-matter"`.

## Images

Relative image paths go through [`ctx.asset`](/docs/ctx/asset), so they get fingerprinted URLs:

```markdown
![Logo](logo.png)
```

## Markdown partials

A `.md` file in a `_private/` folder can be included from EJS with `include()`. It's rendered the same way, EJS then Markdown:

```ejs
<%%- include('intro') %>
```

## Notes

- Layouts are always `+layout.ejs`. A layout can't be Markdown.
