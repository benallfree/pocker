---
title: EJS
description: The EJS tags PocketPages supports, escaping rules, and what's in scope inside a template.
---

# EJS

EJS is always on. There is nothing to install or configure.

## Tags

| Tag | Meaning |
|---|---|
| `<%% code %>` | Run JS, no output |
| `<%%= expr %>` | Output, HTML-escaped |
| `<%%- expr %>` | Output, raw |
| `<%%# comment %>` | Nothing |
| `<%%%` | A literal `<%%` |
| `-%>` | Also remove the newline right after the tag |
| `<%%_` / `_%>` | Also remove whitespace before / after the tag on its line |
| `<script server<%- '>' %> code </script>` | Same as `<%% code %>` |

## Escaping

`<%%= %>` replaces `&` `<` `>` `"` `'` with `&amp;` `&lt;` `&gt;` `&#34;` `&#39;`. `null` and `undefined` output as an empty string.

```ejs
<p><%%= '<b>"x"</b>' %></p>   <!-- <p>&lt;b&gt;&#34;x&#34;&lt;/b&gt;</p> -->
<p><%%- '<b>raw</b>' %></p>    <!-- <p><b>raw</b></p> -->
<p><%%= null %></p>            <!-- <p></p> -->
```

Use `<%%-` only for HTML you trust, such as the output of `include()` or `slot()`.

## In scope

Inside a template you can use:

| Name | |
|---|---|
| `ctx` | The [request context](/docs/ctx) |
| `data` | `ctx.data` |
| `params` | `ctx.params` |
| `include` | Render a [partial](/docs/partials) |
| `slot` | Define or read a [slot](/docs/layouts) |
| `asset` | `ctx.asset` |
| `meta` | `ctx.meta` |

## Output

A template returns its text. There is no `echo()`; anything you want in the output goes through a tag.

```ejs
<ul>
<%% for (const n of [1, 2]) { -%>
  <li><%%= n %></li>
<%% } -%>
</ul>
```

## Caching

A template is compiled once per content hash and reused, so an edited file is recompiled on the next request.
