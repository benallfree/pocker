package edgeroute

import (
	"net/http"
	"regexp"
	"strings"
)

// OpenCORSHeaders allows any origin (for a health route the load balancer and dashboards call cross-origin).
func OpenCORSHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET,HEAD,PUT,PATCH,POST,DELETE,OPTIONS")
	if v := r.Header.Get("Access-Control-Request-Headers"); v != "" {
		h.Set("Access-Control-Allow-Headers", v)
	}
}

// cors is an instance's CORS policy for a forwarded request (the Node firewall's PH_CORS_ORIGINS handling): this
// edge answers preflights and its Access-Control-Allow-Origin is the only one the browser sees.
type cors struct {
	allowOrigin string
}

// corsPolicy returns nil when the instance has no origin list (the owner's own CORS answers).
func corsPolicy(origins []string, r *http.Request) *cors {
	if len(origins) == 0 {
		return nil
	}
	return &cors{allowOrigin: resolveAllowOrigin(origins, r.Header.Get("Origin"))}
}

func (c *cors) apply(h http.Header) {
	appendVary(h, "Origin")
	if c.allowOrigin != "" {
		h.Set("Access-Control-Allow-Origin", c.allowOrigin)
	}
}

func (c *cors) preflight(h http.Header, r *http.Request) {
	if c.allowOrigin == "" {
		return
	}
	appendVary(h, "Access-Control-Request-Method")
	appendVary(h, "Access-Control-Request-Headers")
	h.Set("Access-Control-Allow-Methods", "GET,HEAD,PUT,PATCH,POST,DELETE")
	if v := r.Header.Get("Access-Control-Request-Headers"); v != "" {
		h.Set("Access-Control-Allow-Headers", v)
	}
}

// rewriteUpstream drops the owner's Access-Control-Allow-* headers (ours are already set) and keeps Origin in Vary.
func (c *cors) rewriteUpstream(h http.Header) {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "access-control-allow-") {
			h.Del(k)
		}
	}
	if v := h.Get("Vary"); v != "" {
		hasOrigin := false
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if strings.EqualFold(p, "origin") || p == "*" {
				hasOrigin = true
			}
		}
		if !hasOrigin {
			h.Set("Vary", v+", Origin")
		}
	}
}

func appendVary(h http.Header, v string) {
	var parts []string
	seen := map[string]bool{}
	for _, p := range strings.Split(strings.Join(h.Values("Vary"), ","), ",") {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	if !seen[v] {
		parts = append(parts, v)
	}
	h.Set("Vary", strings.Join(parts, ", "))
}

// resolveAllowOrigin mirrors PocketBase's origin matching: first match in list order wins; "*" allows any origin and
// is echoed as "*"; an exact match is echoed; "*"/"?" inside an entry are wildcards and echo the request origin.
func resolveAllowOrigin(origins []string, origin string) string {
	if origin == "" {
		return ""
	}
	for _, o := range origins {
		if o == "*" {
			return "*"
		}
		if o == origin {
			return origin
		}
	}
	if len(origin) <= 253+3+5 && strings.Contains(origin, "://") {
		for _, o := range origins {
			if strings.ContainsAny(o, "*?") && wildcardRE(o).MatchString(origin) {
				return origin
			}
		}
	}
	return ""
}

func wildcardRE(pattern string) *regexp.Regexp {
	q := regexp.QuoteMeta(pattern)
	q = strings.ReplaceAll(q, `\*`, ".*")
	q = strings.ReplaceAll(q, `\?`, ".")
	return regexp.MustCompile("^" + q + "$")
}
