package tokens

import (
	"sort"
	"strings"
	"sync"
)

// Scope is a named slice of an owner's rights that a token can be limited to. Plugins register their scopes with
// [Tokens.RegisterScope]; a token whose scopes are set authenticates only on the paths of those scopes, and routes
// can double-check with [RequireScope].
type Scope struct {
	// Name, e.g. "deploy" or "files:read". Lower case, no spaces.
	Name string `json:"name"`

	// Description is shown on the consent page and in the token screens.
	Description string `json:"description"`

	// Paths are URL path prefixes (e.g. "/_pocker/deploy/") where a token with this scope authenticates as its
	// owner. Elsewhere a scoped token is refused.
	Paths []string `json:"paths,omitempty"`

	// Collections, when not empty, limits which auth collections may hold a token with this scope (collection
	// names, e.g. "_superusers"). Empty: any collection that may hold tokens.
	Collections []string `json:"collections,omitempty"`
}

// AnyTokenPaths authenticate every token, whatever its scopes: discovery routes that only tell the caller who it is.
var AnyTokenPaths = []string{"/_pocker/whoami"}

type registry struct {
	mu     sync.RWMutex
	scopes map[string]Scope
}

func (r *registry) add(s Scope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scopes == nil {
		r.scopes = map[string]Scope{}
	}
	if old, ok := r.scopes[s.Name]; ok {
		// several plugins may share a scope (deploy + logs under one name): merge their paths
		old.Paths = append(old.Paths, s.Paths...)
		if old.Description == "" {
			old.Description = s.Description
		}
		old.Collections = append(old.Collections, s.Collections...)
		s = old
	}
	r.scopes[s.Name] = s
}

func (r *registry) get(name string) (Scope, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.scopes[name]
	return s, ok
}

func (r *registry) list() []Scope {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Scope, 0, len(r.scopes))
	for _, s := range r.scopes {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// allows reports whether a token with these scopes may authenticate on path. No scopes: everywhere.
func (r *registry) allows(scopes []string, path string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, p := range AnyTokenPaths {
		if pathUnder(path, p) {
			return true
		}
	}
	for _, name := range scopes {
		s, ok := r.get(name)
		if !ok {
			continue
		}
		for _, p := range s.Paths {
			if pathUnder(path, p) {
				return true
			}
		}
	}
	return false
}

func pathUnder(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/")
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
