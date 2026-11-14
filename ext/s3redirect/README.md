# s3redirect

File downloads as short-lived signed redirects, after PocketBase's own access check.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **Status.** Working: Go module `github.com/pockethost/pocketone/ext/s3redirect`, no dependencies beyond
> PocketOne. [`example/`](example/main.go) runs one app with its own in-process bucket, nothing to set up.

## What it does

When an app keeps its files in S3 (Settings → Files storage), PocketBase normally downloads each file from the
bucket and streams it to the browser. With this plugin, `/api/files/...` answers **302** with a presigned GET URL
instead, and the browser fetches the file straight from the bucket. The app's bandwidth, memory and connections stay
free. With Cloudflare R2 the downloads cost no egress either.

- **Access checks don't change.** PocketBase still finds the record, checks protected-file tokens and the view rule,
  and creates thumbnails on first request. The redirect happens only in `OnFileDownloadRequest`, after all of that.
  No core change: the hook already exists upstream.
- **Short-lived:** each URL is valid for `ttl` (default 5 minutes). Anyone holding it can fetch the file until it
  expires, so keep it short for protected files. The file token itself never goes into the bucket URL, and the redirect
  sends `Referrer-Policy: no-referrer`.
- **Same headers as PocketBase:** inline or attachment (`?download=1`), the filename, and forced content types (SVG,
  CSS, JS) are signed into the URL as S3 `response-*` overrides. Range requests work, because S3 serves them.
- **Caching:** the browser may reuse the redirect for 80% of the TTL (`Cache-Control: private`, never shared
  caches); the file itself gets PocketBase's usual 30-day `Cache-Control`.
- **Falls back to streaming** when the app's S3 is off, the plugin is disabled for the app, or signing fails.
- Your own `OnFileDownloadRequest` handlers run first (the plugin binds last, id `__ext_s3redirect__`), so they can
  still change the served file, or answer themselves without calling `e.Next()`.

**Bucket CORS:** a redirect followed by `fetch()` or `<img crossorigin>` from your site needs a CORS rule on the
bucket allowing `GET` from your origin. Plain `<img>`, `<video>` and links need nothing.

**Inline vs attachment:** PocketBase decides this from the stored content type; the plugin goes by the file extension
(it doesn't HEAD the object, so a redirect costs no extra round trip). They agree for every file PocketBase names.

## In the standard PocketOne binary

`examples/pocketone` (the PocketOne release binary) registers it **on by default**: turn on S3 in Settings → Files
storage and downloads redirect. Flags: `--s3Redirect=false` streams as before, `--s3RedirectTTL 2m` changes how long
each URL is valid.

## Single app (your own build)

```go
app := pocketbase.New()
s3redirect.MustRegister(app, s3redirect.Config{TTL: 5 * time.Minute})
app.Start()
```

Then turn on S3 in Settings → Files storage.

## Many apps (pocker)

```go
import "github.com/pockethost/pocketone/ext/s3redirect/pockerplugin"

srv.Plugins = append(srv.Plugins, pockerplugin.New(s3redirect.Config{}))
```

The pocker part is its own package, so single-app builds (like the standard binary) don't link pocker.

```yaml
s3redirect:            # server defaults
  enabled: true        # on unless false
  ttl: 5m

instances:
  - id: abc
    plugins:
      s3redirect: { enabled: false }   # per instance: enabled, ttl
```

Each instance signs for the filesystem its files are served from: with its own S3 settings when that's
PocketBase's S3 filesystem, or through the filesystem's driver when another plugin supplies the bucket through
PocketBase's `OnFilesystemCreate` hook and the driver implements `s3redirect.Signer` (for example
[`ext/storage`](../storage/README.md)'s default bucket, fenced to `instances/<id>/storage/`). Nothing needs wiring:
whatever the files live on, if it can sign, downloads redirect; local disk streams as usual.

Signing is SigV4 query-string auth in [`sigv4`](sigv4/sigv4.go), checked against AWS's documented example. It needs
no change to PocketBase's S3 client.

## Try it

```
cd example && go run . serve
```

The example is its own Go module, so its demo dependencies (gofakes3, the AWS SDK signer) never reach the plugin's
`go.mod`. It:
- starts an S3-compatible bucket in-process on `127.0.0.1:9199` ([gofakes3](https://github.com/johannesboyne/gofakes3),
  objects kept in `pb_data/demo-s3.bolt`), and points the app's Files storage at it;
- seeds a `demo_files` record with an image (thumbs `100x100`) and a protected text file, uploaded through PocketBase;
- prints the URLs to open. Each answers `302` to the bucket.

The bucket only serves presigned GETs whose signature, re-computed with the **AWS SDK's own SigV4 signer**, matches
and hasn't expired, and logs each one (`demo bucket: presigned GET ..., signature valid`). Unsigned or edited URLs get
`403`. So a working download proves the plugin's signatures are AWS-compatible, not just accepted by a lenient fake.

`S3REDIRECT_DEMO=0` skips the demo bucket (use your own Files storage settings); `S3REDIRECT_DEMO_ADDR` moves it.

## License

MIT
