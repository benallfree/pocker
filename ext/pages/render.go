package pages

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
)

//go:embed driver.js
var driverSource string

var driverProgram = goja.MustCompile("ext/pages/driver.js", driverSource, false)

// maxIncludeDepth guards against templates including themselves.
const maxIncludeDepth = 64

// programs caches compiled JS (templates and modules) by kind and content hash. Compiled programs are shared by
// every VM.
type programs struct {
	mu sync.Mutex
	m  map[string]*compiledFile
}

type compiledFile struct {
	prog        *goja.Program
	frontMatter map[string]any // .md pages
	markdown    bool
}

func (c *programs) get(key string) *compiledFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[key]
}

func (c *programs) put(key string, f *compiledFile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*compiledFile{}
	}
	c.m[key] = f
}

// httpError is a ctx.httpError() thrown by page code.
type httpError struct {
	status  int
	message string
}

// request is one page request's state.
type request struct {
	p      *Pages
	e      *core.RequestEvent
	root   *os.Root
	route  *Route
	params map[string]string
	vm     *goja.Runtime
	pp     goja.Value
	cache  *goja.Object // per-VM cache of program functions and module exports

	status   int
	sent     bool
	layouts  bool
	wrapped  string
	slots    map[string]string
	meta     map[string]any
	depth    int
	dev      bool
	plugins  []goja.Value
	builtins map[string]bool
	auth     *core.Record
	flashed  bool
	errorCtx map[string]any
}

// render runs a page or API route in one call into the app's JS VM.
func (p *Pages) render(e *core.RequestEvent, root *os.Root, route *Route, params map[string]string) error {
	t := p.Table()
	dev := p.dev()

	var runErr error
	err := jsvm.Run(p.app, func(vm *goja.Runtime) error {
		r := &request{
			p: p, e: e, root: root, route: route, params: params, vm: vm,
			status: 0, layouts: true, slots: map[string]string{}, meta: map[string]any{}, dev: dev,
		}
		r.cache = perVMCache(vm)
		runErr = r.run(t)
		if runErr != nil {
			r.fail(runErr)
		}
		return nil
	})
	if errors.Is(err, jsvm.ErrNotRegistered) {
		return e.String(http.StatusInternalServerError, "ext/pages: the jsvm plugin must be registered on this app\n")
	}
	if err != nil {
		return err
	}
	return nil
}

func perVMCache(vm *goja.Runtime) *goja.Object {
	if v := vm.Get("__ppCache"); v != nil && !goja.IsUndefined(v) {
		return v.ToObject(vm)
	}
	o := vm.NewObject()
	vm.Set("__ppCache", o)
	return o
}

// run calls the driver.
func (r *request) run(t *Table) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = panicErr(rec)
		}
	}()
	cfg, plugins, err := r.loadConfig(t.Config)
	if err != nil {
		return err
	}
	r.dev = r.dev || cfg.Debug
	r.plugins = plugins
	r.builtins = cfg.Builtins
	r.auth = r.e.Auth
	if r.builtins["auth"] {
		r.readAuthCookie()
	}

	driver, err := r.programFunc("driver", driverProgram)
	if err != nil {
		return err
	}
	_, err = driver(goja.Undefined(), r.rtObject())
	return err
}

func panicErr(rec any) error {
	switch v := rec.(type) {
	case *goja.Exception:
		return v
	case *goja.InterruptedError:
		return v
	case error:
		return v
	default:
		return fmt.Errorf("%v", v)
	}
}

// programFunc runs a compiled program once per VM and caches the function it evaluates to.
func (r *request) programFunc(key string, prog *goja.Program) (goja.Callable, error) {
	v := r.cache.Get(key)
	if v == nil || goja.IsUndefined(v) {
		var err error
		v, err = r.vm.RunProgram(prog)
		if err != nil {
			return nil, err
		}
		r.cache.Set(key, v)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return nil, fmt.Errorf("%s is not a function", key)
	}
	return fn, nil
}

// ---- files ----

func (r *request) readFile(file string) ([]byte, error) {
	b, err := r.root.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return b, nil
}

// template returns the compiled template for file.
func (r *request) template(file string) (*compiledFile, string, error) {
	hash := r.p.hashes.get(r.root, file)
	if hash == "" {
		return nil, "", fmt.Errorf("%s: not found", file)
	}
	key := "t:" + hash
	if c := r.p.programs.get(key); c != nil {
		return c, key, nil
	}
	src, err := r.readFile(file)
	if err != nil {
		return nil, "", err
	}
	text := string(src)
	c := &compiledFile{}
	if path.Ext(file) == ".md" {
		c.markdown = true
		fm, body, err := splitFrontMatter(text)
		if err != nil {
			return nil, "", fmt.Errorf("%s: front matter: %w", file, err)
		}
		c.frontMatter, text = fm, body
	}
	js, err := CompileEJS(text)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", file, err)
	}
	prog, err := goja.Compile(file, js, false)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", file, err)
	}
	c.prog = prog
	r.p.programs.put(key, c)
	return c, key, nil
}

// renderFile renders a template (page, layout or partial) and returns its text.
func (r *request) renderFile(file string, locals goja.Value) string {
	r.depth++
	defer func() { r.depth-- }()
	if r.depth > maxIncludeDepth {
		panic(r.vm.NewGoError(fmt.Errorf("%s: includes nested more than %d deep", file, maxIncludeDepth)))
	}
	c, key, err := r.template(file)
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	for k, v := range c.frontMatter {
		r.meta[k] = v
	}
	fn, err := r.programFunc(key, c.prog)
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	if locals == nil {
		locals = goja.Undefined()
	}
	out, err := fn(goja.Undefined(), r.pp, r.vm.ToValue(file), locals)
	if err != nil {
		panic(err)
	}
	text := out.String()
	if c.markdown {
		text, err = renderMarkdown(text)
		if err != nil {
			panic(r.vm.NewGoError(fmt.Errorf("%s: markdown: %w", file, err)))
		}
	}
	return text
}

// lookupPrivate finds name in _private folders from the folder of from upwards (SPEC 4.2). exts are tried when
// name has no extension.
func (r *request) lookupPrivate(from, name string, exts []string) (string, error) {
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("include %q: invalid name", name)
	}
	candidates := func(dir string) []string {
		base := path.Join(dir, "_private", strings.TrimPrefix(name, "/"))
		if path.Ext(name) != "" {
			return []string{base}
		}
		var out []string
		for _, e := range exts {
			out = append(out, base+e)
		}
		return out
	}
	exists := func(f string) bool {
		st, err := r.root.Stat(f)
		return err == nil && st.Mode().IsRegular()
	}
	if strings.HasPrefix(name, "/") {
		for _, c := range candidates(".") {
			if exists(c) {
				return c, nil
			}
		}
		return "", fmt.Errorf("%q not found in _private/", name)
	}
	dir := path.Dir(from)
	// a file inside a _private folder starts at the folder containing it
	if i := strings.LastIndex("/"+dir+"/", "/_private/"); i >= 0 {
		dir = path.Clean(strings.TrimSuffix(("/" + dir + "/")[:i], "/"))
		dir = strings.TrimPrefix(dir, "/")
		if dir == "" {
			dir = "."
		}
	}
	for {
		for _, c := range candidates(dir) {
			if exists(c) {
				return c, nil
			}
		}
		if dir == "." || dir == "" {
			break
		}
		dir = path.Dir(dir)
	}
	return "", fmt.Errorf("%q not found in any _private/ folder from %s up", name, path.Dir(from))
}

// load runs a JS module (CommonJS) from pb_pages and returns its exports, cached per VM by content.
func (r *request) load(file string) goja.Value {
	hash := r.p.hashes.get(r.root, file)
	if hash == "" {
		panic(r.vm.NewGoError(fmt.Errorf("%s: not found", file)))
	}
	exportsKey := "e:" + file + ":" + hash
	if v := r.cache.Get(exportsKey); v != nil && !goja.IsUndefined(v) {
		return v
	}
	if path.Ext(file) == ".json" {
		src, err := r.readFile(file)
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
		var v any
		if err := json.Unmarshal(src, &v); err != nil {
			panic(r.vm.NewGoError(fmt.Errorf("%s: %w", file, err)))
		}
		val := r.vm.ToValue(v)
		r.cache.Set(exportsKey, val)
		return val
	}
	key := "m:" + hash
	c := r.p.programs.get(key)
	if c == nil {
		src, err := r.readFile(file)
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
		wrapped := "(function (module, exports, require, __filename, __dirname) {\n" + string(src) + "\n})"
		prog, err := goja.Compile(file, wrapped, false)
		if err != nil {
			panic(r.vm.NewGoError(fmt.Errorf("%s: %w", file, err)))
		}
		c = &compiledFile{prog: prog}
		r.p.programs.put(key, c)
	}
	fn, err := r.programFunc(key, c.prog)
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	module := r.vm.NewObject()
	exports := r.vm.NewObject()
	module.Set("exports", exports)
	dir := path.Dir(file)
	requireFn := r.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../") {
			target := path.Clean(path.Join(dir, name))
			if strings.HasPrefix(target, "../") || target == ".." {
				panic(r.vm.NewGoError(fmt.Errorf("%s: require(%q) leaves pb_pages", file, name)))
			}
			for _, cand := range []string{target, target + ".js", target + ".json", target + "/index.js"} {
				if st, err := r.root.Stat(cand); err == nil && st.Mode().IsRegular() {
					return r.load(cand)
				}
			}
			panic(r.vm.NewGoError(fmt.Errorf("%s: require(%q): not found", file, name)))
		}
		global, ok := goja.AssertFunction(r.vm.Get("require"))
		if !ok {
			panic(r.vm.NewGoError(fmt.Errorf("require(%q): no module loader", name)))
		}
		v, err := global(goja.Undefined(), call.Argument(0))
		if err != nil {
			panic(err)
		}
		return v
	})
	if _, err := fn(goja.Undefined(), module, exports, requireFn, r.vm.ToValue(file), r.vm.ToValue(dir)); err != nil {
		panic(err)
	}
	out := module.Get("exports")
	r.cache.Set(exportsKey, out)
	return out
}

// ---- responses ----

func (r *request) isHead() bool { return r.e.Request.Method == http.MethodHead }

func (r *request) write(status int, contentType string, body []byte) {
	if r.sent {
		panic(r.vm.NewGoError(errors.New("a response was already sent")))
	}
	r.sent = true
	h := r.e.Response.Header()
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	r.e.Response.WriteHeader(status)
	if !r.isHead() {
		_, _ = r.e.Response.Write(body)
	}
}

func (r *request) sendPage(html string) {
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	ct := r.e.Response.Header().Get("Content-Type")
	if ct == "" {
		ct = "text/html; charset=utf-8"
	}
	r.write(status, ct, []byte(html))
}

// ---- cookies ----

func encodeURIComponent(s string) string {
	const keep = "-_.!~*'()"
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (r *request) secure() bool {
	return r.e.Request.TLS != nil || strings.EqualFold(r.e.Request.Header.Get("X-Forwarded-Proto"), "https")
}

func (r *request) setCookie(name, value string, opts map[string]any) {
	c := &http.Cookie{Name: name, Value: encodeURIComponent(value), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.secure()}
	applyCookieOpts(c, opts)
	http.SetCookie(r.e.Response, c)
}

func applyCookieOpts(c *http.Cookie, opts map[string]any) {
	for k, v := range opts {
		switch k {
		case "path":
			c.Path = fmt.Sprint(v)
		case "domain":
			c.Domain = fmt.Sprint(v)
		case "maxAge":
			if n, ok := toInt(v); ok {
				if n <= 0 {
					c.MaxAge = -1
				} else {
					c.MaxAge = n
				}
			}
		case "httpOnly":
			c.HttpOnly, _ = v.(bool)
		case "secure":
			c.Secure, _ = v.(bool)
		case "sameSite":
			switch strings.ToLower(fmt.Sprint(v)) {
			case "strict":
				c.SameSite = http.SameSiteStrictMode
			case "none":
				c.SameSite = http.SameSiteNoneMode
			default:
				c.SameSite = http.SameSiteLaxMode
			}
		}
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func (r *request) cookies() map[string]string {
	out := map[string]string{}
	for _, c := range r.e.Request.Cookies() {
		v, err := url.PathUnescape(c.Value)
		if err != nil {
			v = c.Value
		}
		if _, ok := out[c.Name]; !ok {
			out[c.Name] = v
		}
	}
	return out
}

// ---- errors ----

// asHTTPError extracts a ctx.httpError() from a JS exception.
func asHTTPError(err error) (*httpError, bool) {
	var ex *goja.Exception
	if !errors.As(err, &ex) {
		return nil, false
	}
	obj, ok := ex.Value().Export().(map[string]any)
	if !ok {
		if o := ex.Value(); o != nil {
			if oo, ok2 := o.(*goja.Object); ok2 {
				h := oo.Get("__ppHttp")
				if h != nil && !goja.IsUndefined(h) {
					m, _ := h.Export().(map[string]any)
					return httpErrorFrom(m), true
				}
			}
		}
		return nil, false
	}
	if m, ok := obj["__ppHttp"].(map[string]any); ok {
		return httpErrorFrom(m), true
	}
	return nil, false
}

func httpErrorFrom(m map[string]any) *httpError {
	st, _ := toInt(m["status"])
	if st < 400 || st > 599 {
		st = http.StatusInternalServerError
	}
	msg, _ := m["message"].(string)
	return &httpError{status: st, message: msg}
}

// errorWhere returns the template location attached to an exception, if any.
func errorWhere(err error) string {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		if o, ok := ex.Value().(*goja.Object); ok {
			if w := o.Get("__ppWhere"); w != nil && !goja.IsUndefined(w) {
				return w.String()
			}
		}
	}
	return ""
}

// fail answers a request whose page code threw.
func (r *request) fail(err error) {
	if he, ok := asHTTPError(err); ok {
		r.sendError(he.status, he.message, nil)
		return
	}
	where := errorWhere(err)
	r.p.logger().Error("pages: request failed", "path", r.e.Request.URL.Path, "file", r.route.File, "where", where, "error", err.Error())
	r.sendError(http.StatusInternalServerError, "", err)
}

// sendError writes an error response: +error.ejs when there is one (not in dev for 500s), else a plain page.
func (r *request) sendError(status int, message string, cause error) {
	if r.sent {
		return
	}
	// reset what the failed attempt set
	h := r.e.Response.Header()
	h.Del("Content-Type")

	if r.route.Kind == KindAPI {
		if message == "" {
			message = http.StatusText(status)
			if cause != nil && r.dev {
				message = cause.Error()
			}
		}
		body, _ := json.Marshal(map[string]any{"status": status, "message": message})
		r.write(status, "application/json", body)
		return
	}

	if cause != nil && r.dev {
		where := errorWhere(cause)
		var b strings.Builder
		b.WriteString("<!doctype html><html><body><h1>PocketPages error</h1>")
		if where != "" {
			b.WriteString("<p><code>" + html.EscapeString(where) + "</code></p>")
		}
		b.WriteString("<pre><code>" + html.EscapeString(cause.Error()) + "</code></pre></body></html>")
		r.write(status, "text/html; charset=utf-8", []byte(b.String()))
		return
	}

	if message == "" {
		message = "Something went wrong."
		if status != http.StatusInternalServerError {
			message = http.StatusText(status)
		}
	}
	if t := r.p.Table(); t != nil && t.ErrorTpl != "" && r.depth == 0 {
		if out, ok := r.renderErrorPage(t, status, message); ok {
			r.write(status, "text/html; charset=utf-8", []byte(out))
			return
		}
	}
	page := fmt.Sprintf("<!doctype html><html><head><title>%d</title></head><body><p>%s</p></body></html>", status, html.EscapeString(message))
	r.write(status, "text/html; charset=utf-8", []byte(page))
}

// renderErrorPage renders +error.ejs inside the root layout. It returns false if that fails too.
func (r *request) renderErrorPage(t *Table, status int, message string) (out string, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			r.p.logger().Error("pages: +error.ejs failed", "error", fmt.Sprint(rec))
			ok = false
		}
	}()
	if r.pp == nil {
		return "", false
	}
	ctx := r.pp.ToObject(r.vm).Get("ctx").ToObject(r.vm)
	ctx.Set("error", map[string]any{"status": status, "message": message})
	r.slots = map[string]string{}
	r.layouts = true
	html := r.renderFile(t.ErrorTpl, nil)
	if f, err := r.root.Stat("+layout.ejs"); err == nil && f.Mode().IsRegular() {
		r.wrapped = html
		html = r.renderFile("+layout.ejs", nil)
	}
	return html, true
}

// ---- the rt object the driver receives ----

func (r *request) rtObject() *goja.Object {
	vm := r.vm
	e := r.e
	o := vm.NewObject()
	set := func(name string, v any) { _ = o.Set(name, v) }

	layouts := r.route.Layouts
	if layouts == nil {
		layouts = []string{}
	}
	middlewares := r.route.Middlewares
	if middlewares == nil {
		middlewares = []string{}
	}
	set("route", map[string]any{
		"path": r.route.Pattern, "name": r.route.Name, "file": r.route.File, "kind": r.route.Kind.String(),
		"handler": r.route.Handler, "middlewares": middlewares, "layouts": layouts,
	})
	params := map[string]any{}
	for k, v := range r.params {
		params[k] = v
	}
	set("params", params)
	set("method", e.Request.Method)
	u := e.Request.URL
	scheme := "http"
	if r.secure() {
		scheme = "https"
	}
	origin := scheme + "://" + e.Request.Host
	search := ""
	if u.RawQuery != "" {
		search = "?" + u.RawQuery
	}
	set("url", map[string]any{"href": origin + u.RequestURI(), "origin": origin, "pathname": u.Path, "search": search})
	set("query", func() map[string]any {
		out := map[string]any{}
		for k, vs := range u.Query() {
			arr := make([]any, len(vs))
			for i, v := range vs {
				arr[i] = v
			}
			out[k] = arr
		}
		return out
	})
	set("header", func(name string) string { return e.Request.Header.Get(name) })
	set("cookies", func() map[string]string { return r.cookies() })
	set("body", func() any {
		info, err := e.RequestInfo()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return info.Body
	})
	set("files", func(name string) any {
		files, err := e.FindUploadedFiles(name)
		if err != nil {
			return []any{}
		}
		return files
	})
	set("ip", e.RealIP())
	set("event", e)
	if r.auth != nil {
		set("auth", r.auth)
	} else {
		set("auth", goja.Null())
	}
	if r.builtins["realtime"] {
		set("realtime", r.realtimeObject())
	}
	if r.builtins["auth"] {
		set("session", r.sessionObject())
	}
	set("dev", r.dev)

	set("setStatus", func(code int) { r.status = code })
	set("getHeader", func(name string) string { return e.Response.Header().Get(name) })
	set("setHeader", func(name, value string) { e.Response.Header().Set(name, value) })
	set("setCookie", func(name, value string, opts map[string]any) { r.setCookie(name, value, opts) })
	set("clearCookie", func(name string, opts map[string]any) {
		if opts == nil {
			opts = map[string]any{}
		}
		opts["maxAge"] = 0
		r.setCookie(name, "", opts)
	})
	set("redirect", func(status int, location string) {
		if r.sent {
			panic(vm.NewGoError(errors.New("a response was already sent")))
		}
		r.sent = true
		http.Redirect(e.Response, e.Request, location, status)
	})
	set("send", func(status int, contentType, body string) { r.write(status, contentType, []byte(body)) })
	set("sendJSON", func(status int, data goja.Value) {
		var v any
		if data != nil {
			v = data.Export()
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			panic(vm.NewGoError(err))
		}
		r.write(status, "application/json", bytes.TrimRight(buf.Bytes(), "\n"))
	})
	set("sendFile", func(p string) {
		f := path.Clean(strings.TrimPrefix(p, "/"))
		if strings.HasPrefix(f, "..") {
			panic(vm.NewGoError(fmt.Errorf("file %q leaves pb_pages", p)))
		}
		fh, err := r.root.Open(f)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		defer fh.Close()
		body, err := io.ReadAll(fh)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		ct := mime.TypeByExtension(path.Ext(f))
		if ct == "" {
			ct = http.DetectContentType(body)
		}
		r.write(http.StatusOK, ct, body)
	})
	set("sse", func() *goja.Object { return r.sse() })
	set("setLayout", func(on bool) { r.layouts = on })
	set("layoutsOn", func() bool {
		if !r.layouts {
			return false
		}
		ct := e.Response.Header().Get("Content-Type")
		return ct == "" || strings.HasPrefix(ct, "text/html")
	})
	set("sent", func() bool { return r.sent })
	set("methodNotAllowed", func(allow string) {
		e.Response.Header().Set("Allow", allow)
		r.write(http.StatusMethodNotAllowed, "text/plain; charset=utf-8", []byte("Method Not Allowed\n"))
	})
	set("sendPage", func(html string) { r.sendPage(html) })

	set("log", func(level string, args []any) {
		msg := strings.TrimSpace(fmt.Sprintln(args...))
		l := r.p.app.Logger().With("plugin", "pages", "file", r.route.File)
		switch level {
		case "debug":
			if r.dev {
				l.Debug(msg)
			}
		case "info":
			l.Info(msg)
		case "warn":
			l.Warn(msg)
		default:
			l.Error(msg)
		}
	})
	set("metaGet", func(key string) any { return r.meta[key] })
	set("metaSet", func(key string, value any) { r.meta[key] = value })
	set("asset", func(from, ref string) string { return r.p.Asset(path.Dir(from), ref) })
	set("include", func(from, name string, locals goja.Value) string {
		file, err := r.lookupPrivate(from, name, []string{".ejs", ".md"})
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("%s: %w", from, err)))
		}
		return r.renderFile(file, locals)
	})
	set("resolve", func(from, name, mode string) goja.Value {
		file, err := r.lookupPrivate(from, name, []string{"", ".js", ".json"})
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("%s: %w", from, err)))
		}
		switch mode {
		case "require", "":
			return r.load(file)
		case "raw", "script", "style":
			b, err := r.readFile(file)
			if err != nil {
				panic(vm.NewGoError(err))
			}
			s := string(b)
			switch mode {
			case "script":
				s = "<script>\n" + s + "\n</script>"
			case "style":
				s = "<style>\n" + s + "\n</style>"
			}
			return vm.ToValue(s)
		default:
			panic(vm.NewGoError(fmt.Errorf("resolve: unknown mode %q", mode)))
		}
	})
	set("load", func(file string) goja.Value { return r.load(file) })
	set("render", func(file string, locals goja.Value) string { return r.renderFile(file, locals) })
	set("setWrapped", func(s string) { r.wrapped = s })
	set("wrapped", func() string { return r.wrapped })
	set("setSlot", func(name, content string) { r.slots[name] = content })
	set("getSlot", func(name string) string { return r.slots[name] })
	set("setPP", func(v goja.Value) { r.pp = v })
	set("plugins", func() goja.Value {
		items := make([]any, len(r.plugins))
		for i, v := range r.plugins {
			items[i] = v
		}
		return vm.NewArray(items...)
	})
	set("flash", func() goja.Value {
		if r.flashed {
			return goja.Undefined()
		}
		r.flashed = true
		v, ok := r.cookies()["pp_flash"]
		if !ok {
			return goja.Undefined()
		}
		r.setCookie("pp_flash", "", map[string]any{"maxAge": 0})
		return vm.ToValue(v)
	})
	return o
}
