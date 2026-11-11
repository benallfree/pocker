---
title: Using Partials in PocketPages
description: Include reusable templates from _private folders with include(name, locals).
---

# Using Partials in PocketPages

Partials are reusable templates that you include from other templates. They live in `_private/` folders, which are never routed.

## Including a partial

```ejs
<%%- include('nav') %>
<%%- include('card', { product }) %>
```

- `include(name, locals?)` renders a template from a `_private/` folder and returns its text. Use `<%%-` so its HTML isn't escaped.
- `name` may omit the extension; `.ejs` then `.md` are tried.
- The partial sees the same `ctx`, plus `locals` as top-level variables.
- Including more than 64 levels deep is an error.

## Where partials are found

```
pb_pages/
  _private/
    header.ejs
    footer.ejs
  blog/
    _private/
      sidebar.ejs
    posts/
      _private/
        post-card.ejs
      index.ejs
```

PocketPages looks in `_private/` in the current template's folder, then in each parent folder up to `pb_pages/`.

```ejs
<!-- In blog/posts/index.ejs -->
<%%- include('post-card', { post }) %>  <!-- blog/posts/_private/post-card.ejs -->
<%%- include('sidebar') %>              <!-- blog/_private/sidebar.ejs -->
<%%- include('header') %>               <!-- _private/header.ejs -->
```

A name starting with `/` looks only in `pb_pages/_private/`:

```ejs
<%%- include('/header') %>  <!-- always pb_pages/_private/header.ejs -->
```

This means you can:

- Keep section-specific partials in that section's `_private/`.
- Hoist a shared partial up to a common parent.
- Override a parent's partial with a local one of the same name, and still reach the root one with `/name`.

## Passing data

Pass only what the partial needs:

```ejs
<%%- include('user-card', { name: user.getString('name'), joined: user.getString('created') }) %>
```

```ejs
<!-- _private/user-card.ejs -->
<div class="card">
  <h3><%%= name %></h3>
  <p>Joined <%%= joined %></p>
</div>
```

Partials can also read `ctx`, `data` and `params` directly. Passing values explicitly keeps them easy to reuse.

## Markdown partials

A `.md` partial is rendered as EJS, then Markdown:

```ejs
<%%- include('intro') %>  <!-- _private/intro.md -->
```

## JavaScript modules

`_private/` folders also hold JS modules. Load them with [`ctx.resolve()`](/docs/ctx/resolve), which uses the same lookup.
