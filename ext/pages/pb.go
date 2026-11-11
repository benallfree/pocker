package pages

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/dop251/goja"
)

// ctx.pb(): the bundled synchronous PocketBase JS SDK (see sdk/README.md), with a transport that hands each request
// to the app's own router in-process. No network, no port; API rules apply exactly as for a browser.

//go:embed sdk/pocketbase.cjs.js
var sdkSource string

var sdkProgram = goja.MustCompile("ext/pages/sdk/pocketbase.cjs.js",
	"(function (module, exports) {\n"+sdkSource+"\nreturn module.exports;\n})", false)

// sdkBaseURL is the base URL the SDK is created with; only its path matters.
const sdkBaseURL = "http://pocketbase.internal"

// apiHandler returns a handler built from the app's router (the same routes, middlewares and API rules the server
// uses). It's built on first use, after every OnServe hook has registered its routes.
func (p *Pages) apiHandler() (http.Handler, error) {
	p.muxOnce.Do(func() {
		if p.router == nil {
			p.muxErr = errors.New("ctx.pb(): the app isn't serving")
			return
		}
		p.mux, p.muxErr = p.router.BuildMux()
	})
	return p.mux, p.muxErr
}

// sdkClass returns the SDK's Client class in this VM (evaluated once per VM).
func (r *request) sdkClass() goja.Value {
	if v := r.cache.Get("sdk"); v != nil && !goja.IsUndefined(v) {
		return v
	}
	fn, err := r.programFunc("sdkModule", sdkProgram)
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	module := r.vm.NewObject()
	exports := r.vm.NewObject()
	_ = module.Set("exports", exports)
	cls, err := fn(goja.Undefined(), module, exports)
	if err != nil {
		panic(err)
	}
	r.cache.Set("sdk", cls)
	return cls
}

// authToken returns a token for the signed-in record ("" for guests), made once per request.
func (r *request) authToken() string {
	if r.auth == nil {
		return ""
	}
	if r.token == "" || r.tokenFor != r.auth.Id {
		t, err := r.auth.NewAuthToken()
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
		r.token, r.tokenFor = t, r.auth.Id
	}
	return r.token
}

// apiSend is the SDK's transport: same arguments and result shape as $http.send.
func (r *request) apiSend(opts map[string]any) map[string]any {
	raw, _ := opts["url"].(string)
	u, err := url.Parse(raw)
	if err != nil {
		panic(r.vm.NewGoError(fmt.Errorf("ctx.pb(): bad url %q", raw)))
	}
	if !strings.HasPrefix(u.Path, "/api/") {
		panic(r.vm.NewGoError(fmt.Errorf("ctx.pb(): only /api/ routes can be called, not %q", u.Path)))
	}
	method, _ := opts["method"].(string)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	switch b := opts["body"].(type) {
	case nil:
	case string:
		body = strings.NewReader(b)
	default:
		j, err := json.Marshal(b)
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
		body = strings.NewReader(string(j))
	}

	h, err := r.p.apiHandler()
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	req := httptest.NewRequestWithContext(r.e.Request.Context(), method, u.RequestURI(), body)
	req.Host = r.e.Request.Host
	req.RemoteAddr = r.e.Request.RemoteAddr
	for _, k := range []string{"X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP", "X-Forwarded-Proto"} {
		if v := r.e.Request.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	if hs, ok := opts["headers"].(map[string]any); ok {
		for k, v := range hs {
			if v != nil {
				req.Header.Set(k, fmt.Sprint(v))
			}
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	headers := map[string]any{}
	for k := range rec.Header() {
		headers[strings.ToLower(k)] = rec.Header().Get(k)
	}
	text := rec.Body.String()
	var parsed any
	if text != "" {
		_ = json.Unmarshal([]byte(text), &parsed)
	}
	return map[string]any{"statusCode": rec.Code, "headers": headers, "raw": text, "json": parsed}
}

// authRecordJSON is the signed-in record as the SDK's auth store expects it.
func (r *request) authRecordJSON() string {
	if r.auth == nil {
		return "null"
	}
	b, err := json.Marshal(r.auth)
	if err != nil {
		return "null"
	}
	return string(b)
}
