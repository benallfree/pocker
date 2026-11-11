# s3redirect

File downloads as short-lived signed redirects, after PocketBase's own access check.

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/s3redirect`, no dependencies beyond
> PocketBase CE. [`example/main.go`](example/main.go) runs one app with redirects on.

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

## Single app

```go
app := pocketbase.New()
s3redirect.MustRegister(app, s3redirect.Config{TTL: 5 * time.Minute})
app.Start()
```

Then turn on S3 in Settings → Files storage.

## Many apps (pocker)

```go
srv.Plugins = append(srv.Plugins, s3redirect.New(s3redirect.Config{}))
```

```yaml
s3redirect:            # server defaults
  enabled: true        # on unless false
  ttl: 5m

instances:
  - id: abc
    plugins:
      s3redirect: { enabled: false }   # per instance: enabled, ttl
```

Each instance signs with its own S3 settings. `Config.Target` lets Go code pick another bucket for an app (for
example [`ext/storage`](../storage/README.md)'s operator bucket, fenced to `instances/<id>/storage/`); it can't be
set from config files or instance data.

## Testing it locally

With MinIO (`minio server ./data`, `mc mb local/files`), run the example, set Files storage to endpoint
`http://127.0.0.1:9000`, bucket `files`, path style on, upload a file to a record, and open its URL:
`curl -I` shows `302` and a `Location` on the bucket.

## License

MIT
