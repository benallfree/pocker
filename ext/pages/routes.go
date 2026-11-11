package pages

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Kind is what a route serves.
type Kind int

const (
	// KindPage is a template (.ejs, .md).
	KindPage Kind = iota
	// KindAPI is a folder's +api.js.
	KindAPI
	// KindStatic is any other file, served as-is.
	KindStatic
)

func (k Kind) String() string {
	switch k {
	case KindPage:
		return "page"
	case KindAPI:
		return "api"
	default:
		return "static"
	}
}

// Segment is one URL segment of a route: a literal, or a [param].
type Segment struct {
	Literal string
	Param   string // set for [param] segments
}

// Route is one entry of the route table. File paths are slash-separated and relative to pb_pages.
type Route struct {
	Kind     Kind
	Segments []Segment
	Pattern  string // URL pattern without groups, e.g. "/products/[id]" ("/" for the root)
	Name     string // file name without extension ("index", "[id]", "+api")
	File     string

	Handler     string   // +page.js beside a page ("" if none)
	Middlewares []string // +middleware.js files, root first
	Layouts     []string // +layout.ejs files, innermost first
}

// Table is the route table built from one pb_pages folder.
type Table struct {
	Routes []*Route // most specific first

	Config   string // +config.js ("" if none)
	ErrorTpl string // +error.ejs ("" if none)
	NotFound string // +404.ejs ("" if none)
}

var (
	paramRe     = regexp.MustCompile(`^\[([A-Za-z_][A-Za-z0-9_]*)\]$`)
	templateExt = map[string]bool{".ejs": true, ".md": true}
)

// rootOnly are + files allowed only at the top of pb_pages.
var rootOnly = map[string]bool{"+config.js": true, "+error.ejs": true, "+404.ejs": true}

// perFolder are + files allowed in any folder.
var perFolder = map[string]bool{"+layout.ejs": true, "+middleware.js": true, "+page.js": true, "+api.js": true}

// ignored reports whether a path segment never becomes part of a URL and isn't walked.
func ignored(name string) bool {
	return name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") ||
		strings.HasPrefix(name, "_")
}

func isGroup(name string) bool {
	return len(name) > 2 && strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")")
}

// Build walks dir and returns its route table and every problem found. Routes with problems are left out; the rest
// of the table is still returned.
func Build(dir string) (*Table, []error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, []error{err}
	}
	defer root.Close()

	t := &Table{}
	var errs []error
	addErr := func(file, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", file, fmt.Sprintf(format, args...)))
	}

	type folder struct {
		layout, middleware, page, api string
		hasIndexPage                  bool
	}
	folders := map[string]*folder{} // physical folder ("." = root) → its + files
	getFolder := func(d string) *folder {
		f := folders[d]
		if f == nil {
			f = &folder{}
			folders[d] = f
		}
		return f
	}

	var files []string // routable files (pages, statics), slash paths
	walkErr := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			addErr(p, "%v", err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if ignored(name) {
				return fs.SkipDir
			}
			if strings.HasPrefix(name, "+") {
				addErr(p, "folders can't start with +")
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlinks and other non-regular files are never served
		}
		if ignored(name) {
			return nil
		}
		dirName := path.Dir(p)
		if strings.HasPrefix(name, "+") {
			switch {
			case rootOnly[name]:
				if dirName != "." {
					addErr(p, "%s is only allowed at the top of pb_pages", name)
					return nil
				}
				switch name {
				case "+config.js":
					t.Config = p
				case "+error.ejs":
					t.ErrorTpl = p
				case "+404.ejs":
					t.NotFound = p
				}
			case perFolder[name]:
				f := getFolder(dirName)
				switch name {
				case "+layout.ejs":
					f.layout = p
				case "+middleware.js":
					f.middleware = p
				case "+page.js":
					f.page = p
				case "+api.js":
					f.api = p
				}
			default:
				addErr(p, "unknown framework file %s", name)
			}
			return nil
		}
		files = append(files, p)
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}

	// urlSegments turns a physical slash path into URL segments: groups dropped, [param]s parsed.
	urlSegments := func(file string, parts []string) ([]Segment, bool) {
		var segs []Segment
		for _, part := range parts {
			if isGroup(part) {
				continue
			}
			if m := paramRe.FindStringSubmatch(part); m != nil {
				segs = append(segs, Segment{Param: m[1]})
				continue
			}
			if strings.ContainsAny(part, "[]") {
				addErr(file, "%q: a [param] must be a whole segment", part)
				return nil, false
			}
			segs = append(segs, Segment{Literal: part})
		}
		return segs, true
	}

	// chains for a physical folder
	layoutsFor := func(d string) []string {
		var out []string
		for {
			if f := folders[d]; f != nil && f.layout != "" {
				out = append(out, f.layout)
			}
			if d == "." {
				return out
			}
			d = path.Dir(d)
		}
	}
	middlewaresFor := func(d string) []string {
		var chain []string
		for {
			if f := folders[d]; f != nil && f.middleware != "" {
				chain = append(chain, f.middleware)
			}
			if d == "." {
				break
			}
			d = path.Dir(d)
		}
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		return chain
	}

	var routes []*Route
	for _, file := range files {
		dirName := path.Dir(file)
		base := path.Base(file)
		ext := path.Ext(base)
		var parts []string
		if dirName != "." {
			parts = strings.Split(dirName, "/")
		}

		r := &Route{File: file}
		if templateExt[ext] {
			r.Kind = KindPage
			r.Name = strings.TrimSuffix(base, ext)
			if r.Name != "index" {
				parts = append(parts, r.Name)
			} else {
				getFolder(dirName).hasIndexPage = true
			}
			if f := folders[dirName]; f != nil {
				r.Handler = f.page
			}
			r.Middlewares = middlewaresFor(dirName)
			r.Layouts = layoutsFor(dirName)
		} else {
			r.Kind = KindStatic
			r.Name = base
			parts = append(parts, base)
		}
		segs, ok := urlSegments(file, parts)
		if !ok {
			continue
		}
		r.Segments = segs
		routes = append(routes, r)
	}
	for d, f := range folders {
		if f.api == "" {
			continue
		}
		if f.hasIndexPage {
			addErr(f.api, "a folder can't have both +api.js and an index page")
			continue
		}
		var parts []string
		if d != "." {
			parts = strings.Split(d, "/")
		}
		segs, ok := urlSegments(f.api, parts)
		if !ok {
			continue
		}
		routes = append(routes, &Route{
			Kind:        KindAPI,
			Segments:    segs,
			Name:        "+api",
			File:        f.api,
			Handler:     f.api,
			Middlewares: middlewaresFor(d),
		})
	}

	// patterns, reserved prefixes and conflicts
	seen := map[string]*Route{}
	kept := routes[:0]
	for _, r := range routes {
		r.Pattern = patternOf(r.Segments)
		if len(r.Segments) > 0 && r.Segments[0].Param == "" &&
			(r.Segments[0].Literal == "api" || r.Segments[0].Literal == "_") {
			addErr(r.File, "/%s/ belongs to PocketBase; pages can't serve it", r.Segments[0].Literal)
			continue
		}
		key := conflictKey(r.Segments)
		if other := seen[key]; other != nil {
			addErr(r.File, "serves the same URL as %s (%s)", other.File, r.Pattern)
			continue
		}
		seen[key] = r
		kept = append(kept, r)
	}
	routes = kept

	sort.SliceStable(routes, func(i, j int) bool { return moreSpecific(routes[i], routes[j]) })
	t.Routes = routes
	return t, errs
}

func patternOf(segs []Segment) string {
	if len(segs) == 0 {
		return "/"
	}
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		if s.Param != "" {
			b.WriteString("[" + s.Param + "]")
		} else {
			b.WriteString(s.Literal)
		}
	}
	return b.String()
}

// conflictKey ignores param names: /p/[id] and /p/[slug] claim the same URLs.
func conflictKey(segs []Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		if s.Param != "" {
			b.WriteString("[]")
		} else {
			b.WriteString(s.Literal)
		}
	}
	return b.String()
}

// moreSpecific orders routes segment by segment from the left: a literal beats a param. Ties keep file order.
func moreSpecific(a, b *Route) bool {
	n := min(len(a.Segments), len(b.Segments))
	for i := 0; i < n; i++ {
		al, bl := a.Segments[i].Param == "", b.Segments[i].Param == ""
		if al != bl {
			return al
		}
	}
	return len(a.Segments) > len(b.Segments)
}

// Match finds the route for a URL path (already decoded). It returns the route, its params and, for a static file
// requested by its fingerprinted name, fingerprinted=true.
func (t *Table) Match(urlPath string, hashOf func(*Route) string) (r *Route, params map[string]string, fingerprinted bool) {
	parts := splitPath(urlPath)
	for _, cand := range t.Routes {
		if len(cand.Segments) != len(parts) {
			continue
		}
		ok, fp := matchSegments(cand, parts, hashOf)
		if !ok {
			continue
		}
		params = map[string]string{}
		for i, s := range cand.Segments {
			if s.Param != "" {
				params[s.Param] = parts[i]
			}
		}
		return cand, params, fp
	}
	return nil, nil, false
}

func matchSegments(r *Route, parts []string, hashOf func(*Route) string) (ok, fingerprinted bool) {
	last := len(parts) - 1
	for i, s := range r.Segments {
		if s.Param != "" {
			if parts[i] == "" {
				return false, false
			}
			continue
		}
		if s.Literal == parts[i] {
			continue
		}
		if i == last && r.Kind == KindStatic && hashOf != nil && looksFingerprinted(parts[i]) {
			if fingerprint(s.Literal, hashOf(r)) == parts[i] {
				fingerprinted = true
				continue
			}
		}
		return false, false
	}
	return true, fingerprinted
}

// splitPath splits a URL path into segments; "/" and "" give none, and a trailing slash is ignored.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// FingerprintLen is the number of hex characters of a file's SHA-256 in fingerprinted names.
const FingerprintLen = 12

var fingerprintRe = regexp.MustCompile(`\.[0-9a-f]{` + strconv.Itoa(FingerprintLen) + `}(\.|$)`)

func looksFingerprinted(name string) bool { return fingerprintRe.MatchString(name) }

// fingerprint inserts hash before a name's extension: app.css → app.<hash>.css.
func fingerprint(name, hash string) string {
	if hash == "" {
		return name
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i] + "." + hash + name[i:]
	}
	return name + "." + hash
}

// shortHash is the fingerprint of content.
func shortHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:FingerprintLen]
}
