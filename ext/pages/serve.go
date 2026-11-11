package pages

import (
	"net/http"
	"os"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// reserved reports whether a URL path belongs to PocketBase.
func reserved(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/") || p == "/_" || strings.HasPrefix(p, "/_/")
}

// serve is the router middleware: it answers requests for pb_pages routes and passes everything else on.
func (p *Pages) serve(e *core.RequestEvent) error {
	urlPath := e.Request.URL.Path
	if reserved(urlPath) {
		return e.Next()
	}
	t := p.Table()
	if t == nil {
		return e.Next()
	}

	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return e.Next()
	}
	defer root.Close()

	route, params, fingerprinted := t.Match(urlPath, func(r *Route) string { return p.hashes.get(root, r.File) })
	if route == nil {
		return e.Next()
	}

	switch route.Kind {
	case KindStatic:
		if e.Request.Method != http.MethodGet && e.Request.Method != http.MethodHead {
			e.Response.Header().Set("Allow", "GET, HEAD")
			return e.NoContent(http.StatusMethodNotAllowed)
		}
		return p.serveStatic(e.Response, e.Request, root, route, fingerprinted)
	default:
		return p.render(e, route, params)
	}
}

// render runs a page or API route. Rendering (the JS pass: middleware, handlers, EJS, layouts) is the next step of
// the plugin; until then pages answer 501 so a half-built plugin is never mistaken for a working one.
func (p *Pages) render(e *core.RequestEvent, route *Route, params map[string]string) error {
	return e.String(http.StatusNotImplemented, "ext/pages: rendering "+route.Kind.String()+" routes isn't built yet ("+route.File+")\n")
}
