package pages

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// splitFrontMatter removes YAML front matter (between --- lines at the very top) and returns it parsed.
func splitFrontMatter(src string) (map[string]any, string, error) {
	if !strings.HasPrefix(src, "---\n") {
		return nil, src, nil
	}
	end := strings.Index(src[4:], "\n---")
	if end < 0 {
		return nil, src, nil
	}
	raw := src[4 : 4+end]
	rest := src[4+end+4:]
	rest = strings.TrimPrefix(rest, "\n")
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
		return nil, src, err
	}
	return fm, rest, nil
}

// headingIDs implements the SPEC 4.4 id rule: lowercase, keep a-z 0-9 - _ space, spaces to -, repeats get -1, -2.
type headingIDs struct{ seen map[string]int }

var idStrip = regexp.MustCompile(`[^a-z0-9\-_ ]`)
var idSpaces = regexp.MustCompile(`\s+`)

func (h *headingIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	id := strings.ToLower(strings.TrimSpace(string(value)))
	id = idStrip.ReplaceAllString(id, "")
	id = idSpaces.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")
	if id == "" {
		id = "section"
	}
	if n, ok := h.seen[id]; ok {
		h.seen[id] = n + 1
		id = id + "-" + strconv.Itoa(n+1)
	} else {
		h.seen[id] = 0
	}
	return []byte(id)
}

func (h *headingIDs) Put(value []byte) { h.seen[string(value)] = 0 }

// renderMarkdown renders src. asset, when set, rewrites relative image paths (SPEC 4.4).
func renderMarkdown(src string, asset func(string) string) (string, error) {
	b := []byte(src)
	ctx := parser.NewContext(parser.WithIDs(&headingIDs{seen: map[string]int{}}))
	doc := md.Parser().Parse(text.NewReader(b), parser.WithContext(ctx))
	if asset != nil {
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if img, ok := n.(*ast.Image); ok && entering && relativeRef(string(img.Destination)) {
				img.Destination = []byte(asset(string(img.Destination)))
			}
			return ast.WalkContinue, nil
		})
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, b, doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*:`)

// relativeRef reports whether ref is a relative path: no scheme, not //host, not /root, not #fragment or ?query.
func relativeRef(ref string) bool {
	if ref == "" || schemeRe.MatchString(ref) {
		return false
	}
	switch ref[0] {
	case '/', '#', '?':
		return false
	}
	return true
}
