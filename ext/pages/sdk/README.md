# Bundled PocketBase JS SDK (synchronous build)

`pocketbase.cjs.js` is the synchronous JSVM build of the PocketBase JavaScript SDK, used for `ctx.pb()` in pages.

- Source: [benallfree/pocketbase-js-sdk](https://github.com/benallfree/pocketbase-js-sdk), branch `jsvm`, commit
  `c32b392` (npm `pocketbase-js-sdk-jsvm@0.28.10001`, based on upstream js-sdk v0.28.1). A fork of
  [pocketbase/js-sdk](https://github.com/pocketbase/js-sdk).
- License: MIT ([LICENSE.md](LICENSE.md)).
- Update: build the fork's `jsvm` branch (`npm run build`) and copy `dist/pocketbase.cjs.js` here; record the commit.

`ext/pages` doesn't send its requests over the network: each request is handed to the app's own router in-process
(see `pb.go`), so API rules apply exactly as for a browser.
