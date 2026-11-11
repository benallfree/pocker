---
title: ctx.meta
description: A per-request key/value store for page metadata such as title and description.
---

# `ctx.meta(key, value?)`

A per-request key/value store for page metadata (title, description). Set with two arguments, read with one. In templates it's also available as `meta`.

```ejs
<!-- a page -->
<%% meta('title', data.product.getString('name')) %>
<%% meta('description', data.product.getString('summary')) %>
```

```ejs
<!-- +layout.ejs -->
<title><%%= meta('title') || 'My shop' %></title>
<meta name="description" content="<%%= meta('description') %>">
```

Layouts render after the page, so they see what the page set.

## Front matter

In a `.md` page, each top-level key of the YAML front matter is passed to `ctx.meta(key, value)`:

```markdown
---
title: About us
description: Who we are
---
```
