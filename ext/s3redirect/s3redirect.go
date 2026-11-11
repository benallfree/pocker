// Package s3redirect answers PocketBase file downloads with a short-lived signed redirect to the bucket instead of
// streaming the file through the app, when the file lives in S3.
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
// The standard CE binary (cmd/pocketbase) registers it by default. Many apps in one process: see the pockerplugin
// subpackage.
package s3redirect

import (
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
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

	// Target, when set, picks the bucket for an app instead of the app's own S3 settings. Return nil to stream the
	// file as usual. ext/storage uses this to sign for its operator bucket when the app's own S3 is off. Not
	// settable from the config file.
	Target func(app core.App) *Target `yaml:"-"`

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

// SetupApp binds the redirect to app's file downloads. A single app calls it through [Register]; under pocker the
// plugin's InitApp calls it for each instance.
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

// TargetFor returns the bucket downloads of app are signed for, or nil to stream as usual.
func (p *Plugin) TargetFor(app core.App) *Target {
	if p.cfg.Target != nil {
		return p.cfg.Target(app)
	}
	s3 := app.Settings().S3
	if !s3.Enabled {
		return nil
	}
	return &Target{
		Endpoint:       s3.Endpoint,
		Bucket:         s3.Bucket,
		Region:         s3.Region,
		AccessKey:      s3.AccessKey,
		Secret:         s3.Secret,
		ForcePathStyle: s3.ForcePathStyle,
	}
}

func (p *Plugin) handle(e *core.FileDownloadRequestEvent, ttl time.Duration) error {
	if m := e.Request.Method; m != http.MethodGet && m != http.MethodHead {
		return e.Next()
	}
	t := p.TargetFor(e.App)
	if !t.Valid() {
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

	signed, err := t.PresignGet(e.ServedPath, ttl, p.cfg.now(), overrides)
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

// String is for logs.
func (t *Target) String() string {
	if t == nil {
		return "<none>"
	}
	return fmt.Sprintf("%s/%s", strings.TrimRight(t.Endpoint, "/"), t.Bucket)
}
