// Package s3redirect answers PocketBase file downloads with a short-lived signed redirect to the bucket instead of
// streaming the file through the app, when the app's files filesystem can sign URLs (S3).
//
// It asks the filesystem PocketBase serves the download from (the event's Filesystem) to sign, so it works the same for an app's own S3 settings
// and for any plugin that supplies the filesystem through OnFilesystemCreate (e.g. ext/storage's default bucket):
// whatever the app's files live on, if it can sign, downloads redirect; if not (local disk), they stream as usual.
//
// PocketBase still does everything up to the point of sending bytes: it finds the record, checks protected-file
// access (file tokens, view rules), and creates thumbnails on first request. Only then, in OnFileDownloadRequest,
// this plugin replaces the stream with a 302 to a presigned GET URL. The browser fetches the file from the bucket, so
// the app's bandwidth and memory stay free. With Cloudflare R2 there are no egress fees either.
//
// Single app:
//
//	app := pocketbase.New()
//	s3redirect.MustRegister(app, s3redirect.Config{TTL: 5 * time.Minute})
//
// The standard PocketOne binary (examples/pocketone) registers it by default. Many apps in one process: put New(cfg) in
// pocker's Server.Plugins (it's a [pluginkit.Plugin]).
package s3redirect

import (
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"github.com/pockethost/pocketone/ext/s3redirect/sigv4"
)

// Name is the plugin's name: its config key (plugins.s3redirect).
const Name = "s3redirect"

// DefaultTTL is how long a signed URL stays valid when nothing else is configured.
const DefaultTTL = 5 * time.Minute

// HookID is the id of the plugin's OnFileDownloadRequest handler, so an app can unbind it.
const HookID = "__ext_s3redirect__"

// Config is the process-wide configuration.
type Config struct {
	// Enabled is the default for every app: on unless set to false (including the plugin is the opt-in). An app's
	// [AppConfig.Enabled] overrides it.
	Enabled *bool `yaml:"enabled"`

	// TTL is how long each signed URL is valid (default 5m, max 7 days). Keep it short: anyone holding the URL can
	// fetch the file until it expires, including protected files.
	TTL time.Duration `yaml:"ttl"`

	Logger *slog.Logger `yaml:"-"`

	now func() time.Time // tests
}

// AppConfig is one app's configuration.
type AppConfig struct {
	// Enabled overrides [Config.Enabled] when set.
	Enabled *bool `yaml:"enabled"`

	// TTL overrides [Config.TTL] when non-zero.
	TTL time.Duration `yaml:"ttl"`
}

// Plugin signs download redirects for every app it is set up on.
type Plugin struct {
	cfg    Config
	log    *slog.Logger
	ttlSet bool // set in Go: the config file doesn't override it

	sectionOnce sync.Once
}

// New returns the plugin for cfg, filling defaults.
func New(cfg Config) *Plugin {
	ttlSet := cfg.TTL > 0
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultTTL
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Plugin{cfg: cfg, log: log.With("plugin", Name), ttlSet: ttlSet}
}

// Name implements the plugin interface.
func (p *Plugin) Name() string { return Name }

// Config returns the effective process configuration.
func (p *Plugin) Config() Config { return p.cfg }

func (p *Plugin) resolve(ac AppConfig) (enabled bool, ttl time.Duration) {
	enabled = p.cfg.Enabled == nil || *p.cfg.Enabled
	if ac.Enabled != nil {
		enabled = *ac.Enabled
	}
	ttl = p.cfg.TTL
	if ac.TTL > 0 {
		ttl = ac.TTL
	}
	return enabled, ttl
}

// SetupApp binds the redirect to app's file downloads. A single app calls it through the package's Register; the
// plugin's Register method (pocker, pluginkit) calls it for each app.
func (p *Plugin) SetupApp(app core.App, ac AppConfig) error {
	enabled, ttl := p.resolve(ac)
	if !enabled {
		return nil
	}
	app.OnFileDownloadRequest().Bind(&hook.Handler[*core.FileDownloadRequestEvent]{
		Id: HookID,
		// run last, after the app's own handlers (JS or Go) had their chance to change headers or the served file
		Priority: 1000,
		Func: func(e *core.FileDownloadRequestEvent) error {
			return p.handle(e, ttl)
		},
	})
	return nil
}

func (p *Plugin) handle(e *core.FileDownloadRequestEvent, ttl time.Duration) error {
	if m := e.Request.Method; m != http.MethodGet && m != http.MethodHead {
		return e.Next()
	}
	overrides := url.Values{}
	disposition, contentType := responseHeaders(e.Request, e.ServedPath, e.ServedName)
	overrides.Set("response-content-disposition", disposition)
	if contentType != "" {
		overrides.Set("response-content-type", contentType)
	}
	// the signed URL itself is unique per signing, so the browser may keep the bytes as long as PocketBase would
	overrides.Set("response-cache-control", "max-age=2592000, stale-while-revalidate=86400")

	if !e.App.Settings().S3.Enabled && e.App.OnFilesystemCreate().Length() == 0 {
		return e.Next() // local disk and nothing supplying another filesystem: stream as usual, open nothing
	}
	fsys, err := e.App.NewFilesystem() // the files filesystem (S3 settings, or one OnFilesystemCreate supplies)
	if err != nil {
		return e.Next() // PocketBase will report it while streaming
	}
	defer fsys.Close()
	signer := signerFor(e.App, fsys)
	if signer == nil {
		return e.Next() // local disk (or a filesystem that can't sign): stream as usual
	}
	signed, err := signer.SignedURL(e.ServedPath, ttl, p.cfg.now(), overrides)
	if err != nil {
		p.log.Warn("s3redirect: signing failed, streaming instead", "error", err)
		return e.Next()
	}

	h := e.Response.Header()
	// the redirect may be reused by this browser while the URL is still valid, never by shared caches (protected
	// files' redirects carry access)
	if maxAge := int64((ttl - ttl/5) / time.Second); maxAge > 0 {
		h.Set("Cache-Control", "private, max-age="+strconv.FormatInt(maxAge, 10))
	} else {
		h.Set("Cache-Control", "no-store")
	}
	h.Set("Referrer-Policy", "no-referrer") // don't leak the app URL (and a file token) to the bucket host
	return e.Redirect(http.StatusFound, signed)
}

// inline mirrors PocketBase's inlineServeContentTypes: everything else downloads as an attachment.
var inline = map[string]bool{
	"image/png": true, "image/jpg": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/x-icon": true, "image/bmp": true,
	"video/webm": true, "video/mp4": true, "video/3gpp": true, "video/quicktime": true, "video/x-ms-wmv": true,
	"audio/basic": true, "audio/aiff": true, "audio/mpeg": true, "audio/midi": true, "audio/mp3": true,
	"audio/wave": true, "audio/wav": true, "audio/x-wav": true, "audio/x-mpeg": true, "audio/x-m4a": true,
	"audio/aac":       true,
	"application/pdf": true, "application/x-pdf": true,
}

// manual mirrors PocketBase's manualExtensionContentTypes: content types forced by extension.
var manual = map[string]string{
	".svg":  "image/svg+xml",
	".css":  "text/css",
	".js":   "text/javascript",
	".mjs":  "text/javascript",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

// extTypes covers the inline types by extension without depending on the host's mime database.
var extTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".ico": "image/x-icon", ".bmp": "image/bmp",
	".webm": "video/webm", ".mp4": "video/mp4", ".3gp": "video/3gpp", ".mov": "video/quicktime", ".wmv": "video/x-ms-wmv",
	".au": "audio/basic", ".aif": "audio/aiff", ".aiff": "audio/aiff", ".mp3": "audio/mpeg", ".mid": "audio/midi",
	".midi": "audio/midi", ".wav": "audio/wav", ".m4a": "audio/x-m4a", ".aac": "audio/aac",
	".pdf": "application/pdf",
}

// responseHeaders decides Content-Disposition and (when PocketBase would force one) Content-Type the way
// filesystem.System.Serve does. PocketBase decides inline vs attachment from the stored content type; without a HEAD
// request to the bucket the plugin goes by extension, which matches for every file PocketBase names itself.
func responseHeaders(r *http.Request, servedPath, name string) (disposition, contentType string) {
	ext := strings.ToLower(filepath.Ext(servedPath))
	forceAttachment, _ := strconv.ParseBool(r.URL.Query().Get("download"))

	ct := extTypes[ext]
	if ct == "" {
		ct = mime.TypeByExtension(ext)
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = strings.TrimSpace(ct[:i])
		}
	}

	disposition = "attachment"
	if !forceAttachment && inline[ct] {
		disposition = "inline"
	}
	disposition += "; filename=" + strconv.Quote(name)

	if forced, ok := manual[ext]; ok {
		contentType = forced
	}
	return disposition, contentType
}

// Signer is implemented by filesystem drivers that can hand out a time-limited direct URL for a key, such as
// ext/storage's bucket driver. A plugin supplying its own S3 filesystem (OnFilesystemCreate) implements it on its
// driver so downloads from it can be redirected.
type Signer interface {
	SignedURL(key string, ttl time.Duration, now time.Time, overrides url.Values) (string, error)
}

// signerFor returns how to sign fsys's keys: its driver when that's a [Signer], else the app's own S3 settings when
// fsys is PocketBase's S3 filesystem for them; nil when it can't sign (local disk, or an unknown driver).
func signerFor(app core.App, fsys *filesystem.System) Signer {
	drv := fsys.Driver()
	if s, ok := drv.(Signer); ok {
		return s
	}
	st := app.Settings().S3
	if !st.Enabled || !isPocketBaseS3(drv) {
		return nil
	}
	return sigv4.Config{Endpoint: st.Endpoint, Bucket: st.Bucket, Region: st.Region, AccessKey: st.AccessKey,
		SecretKey: st.Secret, PathStyle: st.ForcePathStyle}
}

// isPocketBaseS3 reports whether drv is PocketBase's own S3 driver (tools/filesystem/internal/s3blob), the one
// NewFilesystem builds from the app's S3 settings.
func isPocketBaseS3(drv any) bool {
	t := reflect.TypeOf(drv)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t != nil && strings.HasSuffix(t.PkgPath(), "/tools/filesystem/internal/s3blob")
}
