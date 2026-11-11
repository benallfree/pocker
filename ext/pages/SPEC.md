# PocketPages v1 specification

The contract for `ext/pages`, PocketBase CE's server-rendered multi-page app plugin. The test fixtures
([`fixtures/`](fixtures)), the starter kits and the PocketPages site are written against this document. Where the
implementation and this document disagree, one of them is a bug.

v1 is a clean API. It keeps PocketPages' ideas (a folder of files that become routes, EJS, layouts, middleware,
private partials) and changes the shapes. Apps written for the npm package (0.x) don't run unchanged; "Changes from
0.x" (section 12) lists every difference.

## 1. The `pb_pages` folder

Pages live in their own folder next to PocketBase's others:

```
myapp/
  pb_data/  pb_hooks/  pb_migrations/  pb_public/
  pb_pages/
```

- A single app finds it at `pb_pages` next to `pb_data`, or at `--pagesDir` (a relative path resolves against the
  working directory, like `--hooksDir`).
- Serving is automatic: the standard CE binary includes pages, and an app serves its `pb_pages` whenever the folder
  exists (it starts serving if the folder appears later). There is no on/off switch; no folder, no pages.
- Under the multitenant host every instance has its own `pb_pages`.
- If the folder doesn't exist, the plugin does nothing for that app.
- Page code can `require()` modules from `pb_pages/node_modules`, then from the app's usual module paths. The same
  `require` works in modules, JSX/TSX components and EJS templates:
  - `./x` and `../x` are files in `pb_pages`, relative to the requiring file, never above `pb_pages`. Without an
    extension, `.js`, `.ts`, `.tsx`, `.jsx`, `.json` and `.cjs` are tried, then a folder's `package.json` or `index`.
  - A bare name (`pkg`, `pkg/sub`, `@scope/pkg`) is looked up in the nearest `node_modules` folder from the requiring
    file's folder up to the top of `pb_pages`. A package resolves through its `package.json`: `exports` (a string,
    or conditions, where `require`, then `node`, then `default` are used), else `main`, else `index.js`. With
    `exports`, only what it lists can be required, and no entry may point outside the package.
  - Anything not found that way goes to the app's own module loader.
  - `node_modules` is never routed or served, and isn't watched: after changing files in it, change any file in
    `pb_pages` (or restart) to reload.
- Page code runs in the app's JS VM, so PocketBase's JSVM globals (`$app`, `Record`, `$dbx`, `$security`, ...) are
  available, as far as the sandbox allows for untrusted apps.
- Deploying: `pb_pages` is part of an app's code, next to `pb_hooks` and `pb_migrations` (the `deploy` plugin
  uploads it the same way).
- Pages' own file access (routes, static files, includes, assets) never leaves `pb_pages`.

### 1.1 Special names

A path segment (file or folder name) starting with one of these characters is never a URL:

| Prefix | Meaning |
|---|---|
| `+` | Framework file: `+config`, `+middleware`, `+page`, `+api` (`.js` or `.ts`); `+layout`, `+error`, `+404` (`.ejs`, `.jsx` or `.tsx`). One of each per folder. Any other `+` name is an error at startup. |
| `_` | Private. `_private/` folders hold partials and modules for `include()` and `ctx.resolve()`. Other `_` names are ignored. |
| `-` | Ignored (drafts, notes). |
| `.` | Ignored (dotfiles). |

`node_modules/` is never routed.

### 1.2 Groups

A folder named `(name)` groups files without adding a URL segment: `(marketing)/about.ejs` serves `/about`. Groups can
carry their own `+layout.ejs`, `+middleware.js` and `_private/`.

## 2. Routes

### 2.1 Pages

A **page** is a template file: `.ejs`, `.md`, `.jsx` or `.tsx` (4.5).

| File | URL |
|---|---|
| `index.ejs` | `/` |
| `about.ejs` | `/about` |
| `docs/index.md` | `/docs` (and `/docs/`) |
| `docs/routing.md` | `/docs/routing` |
| `products/[id].ejs` | `/products/123`, with `ctx.params.id = "123"` |
| `[org]/[repo]/index.ejs` | `/acme/web`, with `ctx.params = { org: "acme", repo: "web" }` |

- The extension is dropped. `index` maps to its folder.
- `[name]` matches exactly one non-empty segment. A segment may mix text and a parameter only as the whole segment;
  `post-[id].ejs` is an error at startup.
- A URL with a trailing slash matches the same route as without it. Pages never redirect to add or remove one.
- Matching is case-sensitive.

### 2.2 API routes

A `+api.js` file serves its folder's URL without a template: `feeds/rss/+api.js` serves `/feeds/rss`.
`[param]` folders work as for pages. If a folder has both `+api.js` and an `index` page, startup fails.

### 2.3 Static files

Every other file is served as-is at its path (groups removed): `assets/app.css` serves `/assets/app.css`. Files that
mustn't be served (build inputs such as a Tailwind source) take a `-` prefix: `assets/-app.tailwind.css`.

- Content type from the extension.
- `ETag` from the content hash; `If-None-Match` answers `304`.
- Fingerprinted URLs (see `ctx.asset`, 6.5) are served with `Cache-Control: public, max-age=31536000, immutable`.

### 2.4 Precedence

1. `/api/...` and `/_/...` are PocketBase's. Pages never see them, and a `pb_pages/api/` folder is an error at
   startup.
2. Among matching routes the most specific wins: compare segment by segment from the left; a literal segment beats a
   `[param]`. So `products/new.ejs` beats `products/[id].ejs` for `/products/new`.
3. A static file and a page can't claim the same URL (startup error).
4. A URL nothing in `pb_pages` matches goes on to `pb_public`, then to PocketBase.
5. If `pb_pages/+404.ejs` exists, a `GET`/`HEAD` that nothing (pages, `pb_public`, PocketBase) answers renders it
   inside the root layout with status `404` (never for `/api/` or `/_/`). The root `+middleware.js` runs first;
   page code can still change the status.

The route table is built when the app starts and rebuilt when anything in `pb_pages` changes. A rebuild never
restarts the app or reloads `pb_hooks`. Startup errors (bad names, conflicts) are logged with the file path; the
previous table keeps serving if there was one.

## 3. Request flow

For a URL that matches a page or API route, one JS call does all of this, in order:

1. **Plugins' `onRequest`** (built-in and JS plugins, in config order).
2. **Middleware**, root first: every `+middleware.js` from `pb_pages/` down to the route's folder (groups included).
3. **Handlers**: the folder's `+page.js` (or `+api.js`): `load`, then the handler for the request method.
4. **Template** (pages only), then **layouts**, innermost first.
5. **Response.**

The call runs under the app's usual JS limits (execution time, memory), as one unit. If any step sends a response
(`ctx.res.redirect`, `json`, `html`, `text`, `file`, `sse`), the later steps are skipped.

### 3.1 Middleware

```js
// pb_pages/admin/+middleware.js
module.exports = (ctx, next) => {
  if (!ctx.auth) return ctx.res.redirect('/login')
  ctx.data.section = 'admin'
  return next()
}
```

- One signature: `(ctx, next)`. Call `next()` to continue; return its result.
- Returning without calling `next()` ends the request. If no response was sent, that's an error (500, and in dev
  the error page names the middleware file).
- Share data through `ctx.data`.

### 3.2 Handlers: `+page.js`

```js
// pb_pages/products/[id]/+page.js
module.exports = {
  load(ctx) {
    ctx.data.product = ctx.app.findRecordById('products', ctx.params.id)
  },
  post(ctx) {
    const { qty } = ctx.req.body()
    // ...
    return ctx.res.redirect(`/products/${ctx.params.id}`, { flash: 'Added to cart' })
  },
}
```

- Exports any of `load`, `get`, `post`, `put`, `patch`, `delete`. A page that only renders but must accept a form
  post still needs `post() {}`: methods without a handler answer `405`.
- `load` runs for every method; then the method's handler, if there is one. `HEAD` runs `load` and `get` like
  `GET` (only the body is dropped), so keep side effects in `post`/`put`/`patch`/`delete`.
- A handler may set `ctx.data` fields, or return a plain object, which is shallow-assigned into `ctx.data`
  (`Object.assign`). There is no deep merge.
- A `+page.js` applies to every page in its folder. `ctx.route.name` tells them apart.
- A method with no handler still renders the page for `GET` and `HEAD`. Other methods without a handler answer
  `405` with an `Allow` header listing `GET, HEAD` and the methods that have handlers, in the order
  `GET, HEAD, POST, PUT, PATCH, DELETE`.

### 3.3 API handlers: `+api.js`

Same exports as `+page.js`. There is no template, so a handler must send a response; returning without one is a
500.

```js
// pb_pages/feeds/latest/+api.js
module.exports = {
  get(ctx) {
    const posts = ctx.app.findRecordsByFilter('posts', 'published = true', '-created', 20)
    return ctx.res.json(posts.map((p) => ({ id: p.id, title: p.getString('title') })))
  },
}
```

## 4. Templates

### 4.1 EJS

| Tag | Meaning |
|---|---|
| `<% code %>` | Run JS, no output |
| `<%= expr %>` | Output, HTML-escaped |
| `<%- expr %>` | Output, raw |
| `<%# comment %>` | Nothing |
| `<%%` | A literal `<%` |
| `-%>` | Also remove the newline right after the tag |
| `<%_` / `_%>` | Also remove whitespace before / after the tag on its line |
| `<script server> code </script>` | Same as `<% code %>` |

- Escaping replaces `&` `<` `>` `"` `'` with `&amp;` `&lt;` `&gt;` `&#34;` `&#39;`. `null` and `undefined` output as an empty string.
- A template is compiled once per content hash and reused.

**In scope inside a template:** `ctx`, `data` (= `ctx.data`), `params` (= `ctx.params`), `include`, `slot`, `asset`
(= `ctx.asset`), `meta` (= `ctx.meta`).

A template returns its text. There is no `echo`; anything you want in the output goes through a tag.

### 4.2 Partials: `include`

```ejs
<%- include('nav') %>
<%- include('card', { product }) %>
```

- `include(name, locals?)` renders a template from a `_private/` folder and returns its text. Use `<%-` so its HTML
  isn't escaped.
- Lookup: `_private/` in the current template's folder, then each parent folder up to `pb_pages/`. For a template
  that is itself inside a `_private/` folder, the search starts at the folder containing that `_private/` (so its
  siblings are found first). A name starting with `/` looks only in `pb_pages/_private/`. `..` is not allowed.
- `name` may include its extension; without one, `.ejs` then `.md` are tried.
- The partial sees the same `ctx`, plus `locals` as top-level variables.
- Including more than 64 levels deep is an error (recursion guard).

### 4.3 Slots and layouts

`+layout.ejs` files wrap pages. A page's layouts are every `+layout.ejs` from its own folder up to `pb_pages/`,
applied innermost first. A folder may have one layout.

```ejs
<!-- pb_pages/products/[id].ejs -->
<% slot('title', () => { %><%= data.product.getString('name') %><% }) %>
<h1><%= data.product.getString('name') %></h1>
```

```ejs
<!-- pb_pages/+layout.ejs -->
<html>
  <head><title><%- slot('title') || 'My shop' %></title></head>
  <body><%- slot() %></body>
</html>
```

- `slot(name, fn)` defines a named slot: `fn`'s output is captured and nothing is output in place.
- `slot(name)` returns a slot's content (`''` if not defined). `slot()` returns the content being wrapped: the page,
  or the inner layout.
- Named slots defined by the page or by any inner layout are visible to every outer layout. Defining the same name
  again replaces it; inside the new definition's `fn`, `slot(name)` still returns the previous content, so a layout
  can wrap it (`slot('title', () => { %>Docs: <%- slot('title') %><% })`).
- Layouts are skipped when the response isn't HTML (a handler set another `Content-Type`) or after
  `ctx.res.layout(false)`.
- A page with an inner extension gets that type: `robots.txt.ejs` serves `/robots.txt` as `text/plain` and
  `feed.xml.ejs` serves `/feed.xml` as XML, so layouts are skipped. `.html` (or no inner extension) is HTML. Page
  code can still set another `Content-Type`.

### 4.4 Markdown pages

A `.md` page is EJS first, then Markdown (CommonMark + GFM tables, strikethrough, task lists, autolinks).

- Front matter between `---` lines at the top is YAML, removed before EJS runs. Each top-level key is passed to
  `ctx.meta(key, value)`, for pages and for included `.md` partials.
- Headings get `id`s: lowercased, characters other than `a-z 0-9 - _ space` removed, spaces to `-`; a repeated id
  gets `-1`, `-2`, ... appended.
- Fenced code blocks render as `<pre><code class="language-<lang>">`. Raw HTML passes through unchanged.
- Relative image paths go through `ctx.asset`.
- A `.md` partial is rendered (EJS, then Markdown) on its own and inserted as HTML; Markdown never runs twice on the
  same text. Leave blank lines around an `include` in Markdown so the HTML stays a block.

### 4.5 JSX and TSX

`.jsx` and `.tsx` files are templates too: pages, layouts (`+layout.tsx`), `+error`/`+404`, and partials in
`_private/`. They're compiled when first used by esbuild, which is built into the binary (no Node, no build step), and
cached by content like EJS. TypeScript types are stripped, not checked.

```tsx
// pb_pages/products/[id].tsx
import Price from './_private/Price'

export default function Product({ data, slot }: any) {
  slot('title', data.product.getString('name'))
  return (
    <article class="product">
      <h1>{data.product.getString('name')}</h1>
      <Price cents={data.product.getInt('price')} />
    </article>
  )
}
```

- The file's default export is a component: a function from props to markup. Props: `ctx`, `data`, `params`,
  `meta`, `asset`, `include`, `slot`, `children`, plus an `include`'s locals.
- JSX renders straight to HTML on the server. There's no virtual DOM, no hydration, no client-side React. Event
  handler props (functions) are dropped; use plain attributes (`onclick="..."`, `hx-get`, `data-on:click`).
- Text and attribute values are escaped. `dangerouslySetInnerHTML={{ __html }}` inserts raw HTML. `class` and
  `className` both work, as do `for` and `htmlFor`. `style` takes a string or an object (`{ marginTop: 4 }` →
  `margin-top:4`). `true` renders a bare attribute; `false`, `null` and `undefined` leave it out.
- `<html>` renders with `<!doctype html>` in front of it (JSX can't write a doctype).
- In a layout, `children` is the inner content. `slot(name)` reads a named slot, `slot(name, content)` sets one
  (content is JSX or a string), `slot()` is the same as `children`.
- `include(name, locals)` returns the partial's HTML, ready to put in JSX. EJS can `include` a `.jsx`/`.tsx` partial:
  its locals become props.
- Components are ordinary modules: `import Card from './_private/Card'` (resolved like `require`, 3.2). Partials
  are looked up with extensions in this order: `.ejs`, `.md`, `.jsx`, `.tsx`.

**TypeScript modules.** `+page.ts`, `+api.ts`, `+middleware.ts`, `+config.ts` and any `.ts` file reached by
`require`/`import` work like their `.js` versions. A module whose only export is `export default` behaves like
`module.exports = ...`.

## 5. Responses

| Situation | Response |
|---|---|
| A handler or middleware sent one | That one |
| Page rendered | `200 text/html; charset=utf-8` (or the status set with `ctx.res.status`) |
| `HEAD` | As `GET`, without a body |
| Handler for another method missing | `405` + `Allow` |
| Error thrown | `500` (see 8) |

JSON is only sent through `ctx.res.json` (or `+api.js`). Template output is never parsed as JSON.

## 6. The `ctx` object

One object per request, passed to middleware, handlers, templates and plugins.

### 6.1 `ctx.req`

| Member | |
|---|---|
| `method` | `"GET"`, `"POST"`, ... |
| `url` | `{ href, origin, pathname, search }` |
| `query(name)` | First value of a query parameter as a string, or `undefined` |
| `query(name, { all: true })` | Every value, as an array of strings |
| `query()` | `{ name: firstValue }` for every parameter |
| `header(name)` | Request header, `''` if absent |
| `cookie(name)` | Cookie value as a string, or `undefined` |
| `cookie(name, { json: true })` | Cookie parsed as JSON (`undefined` if absent or not JSON) |
| `cookies()` | `{ name: value }`, strings |
| `body()` | Parsed body: JSON object, or form fields (`application/x-www-form-urlencoded`, `multipart/form-data`) as `{ name: value }` (repeated names as arrays) |
| `files(name)` | Uploaded files for a multipart field, as PocketBase `*filesystem.File` values ready for a record field |
| `ip` | Client IP as PocketBase resolves it (trusted proxy settings apply) |
| `event` | The underlying PocketBase `RequestEvent`, for anything not covered here |

Query values and cookies are strings unless you ask for JSON. Nothing is parsed for you.

### 6.2 `ctx.res`

| Member | |
|---|---|
| `status(code)` | Status for the rendered page. Returns `ctx.res`. |
| `header(name, value)` | Set a response header; returns `ctx.res`. `header(name)` reads it back. |
| `cookie(name, value, options?)` | Set a cookie. Non-string values are `JSON.stringify`'d; the value is then percent-encoded (`encodeURIComponent`), and `ctx.req.cookie` decodes it. Defaults: `path=/`, `HttpOnly`, `SameSite=Lax`, `Secure` when the request is HTTPS. Options: `path`, `domain`, `maxAge` (seconds), `expires` (Date), `httpOnly`, `secure`, `sameSite`. |
| `clearCookie(name, options?)` | Expire a cookie (same `path`/`domain` as when set). |
| `redirect(location, { status = 302, flash }?)` | Send a redirect. `flash` sets a one-time message (6.4). |
| `json(data)` / `json(status, data)` | Send JSON. |
| `html(html)` / `html(status, html)` | Send HTML as-is (no layouts). |
| `text(text)` / `text(status, text)` | Send plain text. |
| `file(path)` | Send a file from `pb_pages` (path relative to `pb_pages`). |
| `sse()` | Start a `text/event-stream` response; returns `{ send(event, data, { id, retry }?), comment(text), close() }`. |
| `layout(false)` | Render this page without layouts (e.g. an HTML fragment for htmx). |
| `sent` | `true` once a response has been sent. |

### 6.3 Data and routing

| Member | |
|---|---|
| `ctx.data` | Plain object shared by middleware, handlers and templates. Starts as `{}`. |
| `ctx.params` | Route parameters, strings. Never includes query parameters. |
| `ctx.route` | `{ path, name, file }`: the URL pattern without groups (`/products/[id]`, `/docs` for `docs/index.md`), the file name without extension (`[id]`, `index`; `+api` for API routes) and the file relative to `pb_pages`, groups included. |

### 6.4 Flash messages

- `ctx.res.redirect(path, { flash: 'Saved' })` stores the message as JSON in a cookie (`pp_flash`, encoded like any
  cookie, `HttpOnly`, `Max-Age=300`).
- `ctx.flash()` on the next request returns it and clears it (`Max-Age=0`). It returns `undefined` when there is
  none. Calling it again in the same request returns the same value.
- A flash can be any JSON value.

### 6.5 Helpers

| Member | |
|---|---|
| `ctx.auth` | The authenticated record, or `null`. Set by PocketBase's own auth (an `Authorization` header) or by the `auth` plugin's cookie. |
| `ctx.app` | The app (`$app`): records, collections, settings. |
| `ctx.pb()` | A PocketBase JS SDK client (the bundled synchronous build) acting as the signed-in user (`ctx.auth`), or as a guest. Its requests go to the app's own API in-process (no network), so collection API rules, `expand` and field visibility apply exactly as for a browser. API errors throw the SDK's `ClientResponseError` (`e.status`, `e.response`). Only `/api/` routes; file uploads through the SDK aren't supported (use `ctx.app`). |
| `ctx.env(name)` | Environment variable, `''` if unset (the app's environment; under the sandbox, the instance's). |
| `ctx.log` | `debug`, `info`, `warn`, `error`: to the app's log. `debug` only shows in dev. |
| `ctx.meta(key, value?)` | Per-request key/value store for page metadata (title, description). Set with two arguments, read with one. |
| `ctx.asset(path)` | URL for a static file in `pb_pages`, with its content hash: `asset('app.css')` → `/assets/app.3f9a1c2b7d4e.css`. Relative paths resolve from the current file's folder; `/`-paths from `pb_pages`. A path that isn't a file in `pb_pages` is returned unchanged. |
| `ctx.include(name, locals?)` | Render a `_private/` partial from JS (handlers, middleware, plugins) and get its text; same lookup as the template `include`, from the route's folder. |
| `ctx.resolve(name, { mode }?)` | Load a module from `_private/` (same lookup as `include`; without an extension, the name, then `.js`, then `.json` are tried). Modes: `require` (default, the module's exports), `raw` (file text), `script` (text wrapped in `<script>`), `style` (wrapped in `<style>`). |
| `ctx.store(name, value?)` | The app's in-memory store (`$app.store()`): set with two arguments, read with one. |
| `ctx.dev` | `true` in dev mode. |

Fingerprinted asset names carry the first 12 hex characters of the file's SHA-256: `name.<hash>.ext`.

## 7. Configuration: `+config.js`

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['auth', 'realtime', require('./_private/datastar.js')],
  debug: false,
}
```

| Key | Default | |
|---|---|---|
| `plugins` | `[]` | Built-ins by name (`auth`, `realtime`), or JS plugin factories such as add-ons (9). EJS and Markdown are always on. |
| `debug` | `false` | Detailed error pages and `ctx.log.debug` output even outside dev mode. |

Unknown keys, unknown built-in names and duplicates are startup errors.

## 8. Errors

- Anything thrown in middleware, a handler or a template becomes a `500`.
- **Dev mode or `debug: true`:** an HTML error page with the message, the file and line (template lines map to the
  `.ejs`/`.md` source), and the stack trimmed to `pb_pages`-relative paths.
- **Otherwise:** a plain "Something went wrong" page. The full error always goes to the app's log.
- `throw ctx.httpError(status, message)` sends that status (e.g. `404`, `403`) with a plain page showing `message`.
  In a `+api.js` it's JSON: `{ "status": 404, "message": "..." }`.
- A custom error page: `pb_pages/+error.ejs` renders for every `ctx.httpError` and every 500 outside dev mode, with
  `ctx.error = { status, message }`, inside the root layout.

## 9. Plugins

### 9.1 Built-in

Turned on by name in `+config.js`.

**`auth`:** cookie sessions on PocketBase auth, without calling the app over HTTP.
- Every request: reads the `pb_auth` cookie, validates the token, sets `ctx.auth` (or clears a bad cookie).
- The cookie holds `{ token, record }` as JSON (the JS SDK's cookie format), `HttpOnly`, `SameSite=Lax`, `Secure` on
  HTTPS, lasting as long as the collection's auth token.
- `identity` arguments accept any of the collection's identity fields (email by default).
- `ctx.session.authMethods({ collection })` → `{ password: { enabled, identityFields }, otp: { enabled }, mfa:
  { enabled }, oauth2: { enabled, providers: [{ name, displayName }] } }`, as PocketBase's auth-methods API.
- `ctx.session.register(email, password, { collection = 'users', data, verify = true, signIn = true })`: creates the
  record; sends the verification email when `verify`; signs in when `signIn`.
- `ctx.session.signInWithPassword(identity, password, { collection })`
- `ctx.session.requestOTP(email, { collection, create = false })` → `otpId`. Always returns an id (unknown emails
  get one that never validates, as PocketBase does); with `create: true` a missing record is created first.
- `ctx.session.signInWithOTP(otpId, code, { collection })`
- `ctx.session.signInAnonymously({ collection })`
- `ctx.session.requestOAuth2(provider, { collection, redirectPath = '/auth/oauth/confirm', redirect = true })`:
  stores state in a cookie and returns the provider's URL; with `redirect` it also sends the redirect
  (`ctx.res.sent`). `redirectPath` must be a path on this site. PKCE is used when the provider supports it.
- `ctx.session.signInWithOAuth2(state, code, { createData }?)`: completes it on the redirect path (the state cookie
  carries the provider, collection and PKCE verifier; it lasts 10 minutes and works once). The sign-in runs through
  PocketBase's own `auth-with-oauth2` endpoint in-process, so sign-up, account linking (to the signed-in account,
  if any), the anti-hijacking checks and `onRecordAuthWithOAuth2Request` hooks behave as in PocketBase. `createData`
  is extra data for a new record. Returns `{ token, record, meta }` (`meta` is PocketBase's OAuth2 meta, with
  `isNew`). Accounts that need MFA throw an error: pages doesn't do MFA yet.
- Apple answers with a POST, so read `state` and `code` from `ctx.req.body()` there. On HTTPS the state cookie is
  `SameSite=None` so it survives that cross-site POST.
- `ctx.session.signIn(record)`: set the cookie for a record you authenticated yourself.
- `ctx.session.signOut()`
- Email flows, with PocketBase's own checks and mail templates: `requestVerification(email, { collection })`,
  `confirmVerification(token)`, `requestPasswordReset(email, { collection })`,
  `confirmPasswordReset(token, password, passwordConfirm)`, `requestEmailChange(newEmail)` (needs `ctx.auth`),
  `confirmEmailChange(token, password)`. Request methods never reveal whether an email exists.
- Every sign-in method sets `ctx.auth` for the rest of the request and returns `{ token, record }`.

**`realtime`:** PocketBase realtime from page code.
- `ctx.realtime.send(topic, data, { filter }?)` sends to every client subscribed to `topic` (the filter only narrows
  that set). `data` is always
  JSON-encoded (a string arrives as a JSON string, as PocketBase's own messages do).
- `filter(client)` returns `true` to include a client. `client` is PocketBase's realtime client: `id()`,
  `get(key)`, `set(key, value)`, `hasSubscription(topic)`.
- `ctx.realtime.client(id)` returns one client by id, or `null`.
- Sending never waits for slow clients: each message is handed to the client's queue in the background.

### 9.2 JS plugins

A JS plugin is a factory that returns hooks:

```js
module.exports = (config) => ({
  name: 'my-plugin',
  onRequest(ctx) {},                       // before middleware
  extendContext(ctx) { ctx.hello = () => 'hi' },
  handles(file) { return file.endsWith('.txt.ejs') }, // treat extra files as EJS pages
  transform({ html, ctx, file }) { return html },     // after each page or layout renders (not partials)
})
```

- `config` is the plugin's entry in `+config.js` when it's an object (`{ factory, ...options }`), else `{}`.
- `name` is required and unique.
- A file a plugin `handles` is an EJS page whose URL drops only its last extension (`robots.txt.ejs` →
  `/robots.txt`).
- Hooks run in config order. Every hook is optional.
- JS plugins run with the same permissions as the rest of the app's JS (sandboxed in a multitenant host).

### 9.3 Add-ons

Plugins for third-party libraries ship as JS files in `ext/pages/addons/`, not built into `ext/pages`. Copy one into
`pb_pages` (usually `_private/`) and list it in `+config.js` with `require`.

**`datastar.js`** ([Datastar](https://data-star.dev)) adds `ctx.datastar`:
- `ctx.datastar.scripts({ spa: { scope, selector }, realtime }?)` returns the script tags (`spa` turns on
  client-side navigation for links under `scope`, swapping `selector`). With `realtime: true` the page subscribes to
  the `datastar` realtime topic and sets a `$clientId` signal to its realtime client id.
- `ctx.datastar.patchElements(html, { selector, mode }?)`, `ctx.datastar.patchSignals(signals, { onlyIfMissing }?)`
  (`signals` is an object), `ctx.datastar.executeScript(js)` send SSE events on the current response. The first one
  starts the stream (`ctx.res.sent` becomes `true`); later steps still run, but their output isn't sent and the
  automatic patch below doesn't happen. The stream closes when the request ends.
- `ctx.datastar.signals()` returns the request's signals as an object (from the `datastar` query parameter on
  `GET`, the JSON body otherwise).
- `mode` is one of `outer` (default), `inner`, `replace`, `prepend`, `append`, `before`, `after`, `remove`.
- When a request has `Datastar-Request: true`, the rendered page (without layouts) is sent as a `patchElements`
  event, into `Datastar-Selector` if given.
- `ctx.datastar.realtime.patchElements(html, options?, { filter }?)`, `.patchSignals(signals, options?, { filter }?)`
  and `.executeScript(js, { filter }?)` send the same events to `datastar` realtime subscribers (needs `realtime`).

## 10. Dev mode

On when the app runs in dev mode (`--dev`) or `+config.js` sets `debug: true`:
- detailed error pages (8);
- `ctx.log.debug` output;
- template cache keyed by content hash, so edits show on the next request (the route table is rebuilt on any file
  change in every mode).

## 11. Starter kits

`pocketbase pages create <name> [--starter <kit>]` creates the folder `<name>` (it must not exist) and copies the
whole kit into it; `pb_data` appears on the first `pocketbase serve`. Kits: `minimal` (default), `mvp`, `htmx`,
`daisyui`, `daisyui-docs`, `datastar`, `auth`. The command needs a build that includes `ext/pages`. It copies
`pb_pages/` plus anything beside it (`pb_migrations/`, `README.md`, a `package.json` for CSS tooling). Kits ship
inside `ext/pages` (`starters/`).

## 12. Changes from 0.x (npm PocketPages)

| 0.x | v1 |
|---|---|
| `pb_hooks/pages/`, booted by `require('pocketpages')` in a `.pb.js` | `pb_pages/`, no boot file, no npm install |
| `api` object, re-spread into each call | `ctx` with `req`, `res`, `data`, `params`, `route`, helpers |
| Query parameters mixed into `params`, JSON-parsed | `ctx.params` is route parameters only; `ctx.req.query()` returns strings |
| Cookies JSON-parsed on read | Strings; `{ json: true }` to parse |
| Middleware: return a value, or call `next(extra)`, chosen by arity | `(ctx, next)`, data on `ctx.data` |
| `+load.js`, `+get.js`, `+post.js`, ... per folder, results deep-merged | One `+page.js` exporting `load`, `get`, `post`, ...; returned objects shallow-assigned |
| Template output that parses as JSON is sent as JSON | Only `ctx.res.json` or `+api.js` |
| `<!-- slot:name -->` comments; layouts read `slot` / `slots` | `slot(name, fn)` to define, `slot(name)` / `slot()` to read |
| `echo()` writes during render | Templates return text; streaming only through `ctx.res.sse()` |
| `redirect(path, { message })` puts the flash in `?__flash=` | `ctx.res.redirect(path, { flash })`, one-time cookie, `ctx.flash()` |
| `asset()` fingerprint: full SHA-256 | First 12 hex characters |
| `+config.js` plugins: npm package names, EJS options | Built-in names or JS factories; EJS and Markdown always on |
| auth plugin through the JS SDK over HTTP (`api.pb()`) | `ctx.session.*`, direct app calls |
| `url()`, `stringify()`, micro-dash globals | Removed: use `ctx.req.url`, `JSON.stringify`, plain JS |
| `pb()` (js-sdk plugin over HTTP to `localhost:8090`) | `ctx.pb()`: same SDK, built in, in-process, as the signed-in user |
| Route with fewer segments could match a longer URL | Segment counts must match |
| `create-pocketpages` | `pocketbase pages create <name> [--starter <kit>]` |
