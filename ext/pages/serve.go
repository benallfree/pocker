package pages

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

// reserved reports whether a URL path belongs to PocketBase.
func reserved(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/") || p == "/_" || strings.HasPrefix(p, "/_/")
}

// isNotFound reports whether err is the 404 PocketBase answers when nothing (pb_public, routes) matched.
func isNotFound(err error) bool {
	var apiErr *router.ApiError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
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
		err := e.Next()
		if t.NotFoundRoute != nil && isNotFound(err) && !e.Written() &&
			(e.Request.Method == http.MethodGet || e.Request.Method == http.MethodHead) {
			return p.renderStatus(e, root, t.NotFoundRoute, map[string]string{}, http.StatusNotFound)
		}
		return err
	}

	switch route.Kind {
	case KindStatic:
		if e.Request.Method != http.MethodGet && e.Request.Method != http.MethodHead {
			e.Response.Header().Set("Allow", "GET, HEAD")
			return e.NoContent(http.StatusMethodNotAllowed)
		}
		return p.serveStatic(e.Response, e.Request, root, route, fingerprinted)
	default:
		return p.render(e, root, route, params)
	}
}
