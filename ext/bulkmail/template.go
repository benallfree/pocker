package bulkmail

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// Templates are deliberately small: {{path}} placeholders and {{if path}}…{{else}}…{{end}} blocks (with
// {{if !path}}), nothing else. Paths are dotted: record.name, campaign.name, email, unsubscribe_url, or a var.
// Values are HTML-escaped in HTML bodies and inserted as is in subjects and text bodies.

type tmplNode struct {
	text   string     // literal text (kind 0)
	path   string     // placeholder or condition path
	negate bool       // {{if !path}}
	kind   int        // 0 text, 1 placeholder, 2 if
	then   []tmplNode // if branch
	els    []tmplNode // else branch
}

var tagRe = regexp.MustCompile(`\{\{\s*(.*?)\s*\}\}`)

// parseTemplate parses src into nodes, or reports the first syntax error.
func parseTemplate(src string) ([]tmplNode, error) {
	type frame struct {
		node   *tmplNode
		inElse bool
	}
	var root []tmplNode
	var stack []*frame

	appendNode := func(n tmplNode) {
		if len(stack) == 0 {
			root = append(root, n)
			return
		}
		f := stack[len(stack)-1]
		if f.inElse {
			f.node.els = append(f.node.els, n)
		} else {
			f.node.then = append(f.node.then, n)
		}
	}

	pos := 0
	for _, m := range tagRe.FindAllStringSubmatchIndex(src, -1) {
		if m[0] > pos {
			appendNode(tmplNode{text: src[pos:m[0]]})
		}
		pos = m[1]
		tag := src[m[2]:m[3]]

		switch {
		case strings.HasPrefix(tag, "if "):
			cond := strings.TrimSpace(tag[3:])
			n := &tmplNode{kind: 2}
			if strings.HasPrefix(cond, "!") {
				n.negate = true
				cond = strings.TrimSpace(cond[1:])
			}
			if !validPath(cond) {
				return nil, fmt.Errorf("invalid condition %q", tag)
			}
			n.path = cond
			stack = append(stack, &frame{node: n})
		case tag == "else":
			if len(stack) == 0 || stack[len(stack)-1].inElse {
				return nil, fmt.Errorf("unexpected {{else}}")
			}
			stack[len(stack)-1].inElse = true
		case tag == "end":
			if len(stack) == 0 {
				return nil, fmt.Errorf("unexpected {{end}}")
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			appendNode(*f.node)
		default:
			if !validPath(tag) {
				return nil, fmt.Errorf("invalid placeholder {{%s}}", tag)
			}
			appendNode(tmplNode{kind: 1, path: tag})
		}
	}
	if len(stack) > 0 {
		return nil, fmt.Errorf("missing {{end}} for {{if %s}}", stack[len(stack)-1].node.path)
	}
	if pos < len(src) {
		appendNode(tmplNode{text: src[pos:]})
	}
	return root, nil
}

var pathRe = regexp.MustCompile(`^[A-Za-z_][\w]*(\.[A-Za-z_][\w]*)*$`)

func validPath(p string) bool { return pathRe.MatchString(p) }

// renderTemplate renders src with data. escape HTML-escapes placeholder values.
func renderTemplate(src string, data map[string]any, escape bool) (string, error) {
	nodes, err := parseTemplate(src)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	renderNodes(&b, nodes, data, escape)
	return b.String(), nil
}

func renderNodes(b *strings.Builder, nodes []tmplNode, data map[string]any, escape bool) {
	for _, n := range nodes {
		switch n.kind {
		case 0:
			b.WriteString(n.text)
		case 1:
			v := toString(lookup(data, n.path))
			if escape {
				v = html.EscapeString(v)
			}
			b.WriteString(v)
		case 2:
			if truthy(lookup(data, n.path)) != n.negate {
				renderNodes(b, n.then, data, escape)
			} else {
				renderNodes(b, n.els, data, escape)
			}
		}
	}
}

func lookup(data map[string]any, path string) any {
	var cur any = data
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		return s != "" && s != "0" && s != "false"
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return toString(v) != ""
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []string:
		return strings.Join(x, ", ")
	case types.JSONRaw: // json fields, and a view's expression columns: show the value, not its JSON
		var decoded any
		if len(x) == 0 || json.Unmarshal(x, &decoded) != nil {
			return x.String()
		}
		return toString(decoded)
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

var legacyVarRe = regexp.MustCompile(`\{\$(\w+)\}`)

// ConvertLegacyTemplate rewrites the legacy PocketHost mailer's {$var} placeholders to {{var}}.
func ConvertLegacyTemplate(src string) string {
	return legacyVarRe.ReplaceAllString(src, "{{$1}}")
}

// footerHTML and footerText are appended to every campaign message (CAN-SPAM and the Gmail/Yahoo bulk sender rules).
func footerHTML(unsubscribeURL, postal string) string {
	var b strings.Builder
	b.WriteString(`<hr style="border:0;border-top:1px solid #ddd;margin:24px 0 12px"><p style="font-size:12px;color:#777">`)
	b.WriteString(`Don't want these emails? <a href="` + html.EscapeString(unsubscribeURL) + `">Unsubscribe</a>.`)
	if postal = strings.TrimSpace(postal); postal != "" {
		b.WriteString("<br>" + strings.ReplaceAll(html.EscapeString(postal), "\n", "<br>"))
	}
	b.WriteString("</p>")
	return b.String()
}

func footerText(unsubscribeURL, postal string) string {
	s := "\n\n--\nUnsubscribe: " + unsubscribeURL
	if postal = strings.TrimSpace(postal); postal != "" {
		s += "\n" + postal
	}
	return s
}

var (
	htmlBreakRe = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</h[1-6]>|</li>`)
	htmlTagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	blankRe     = regexp.MustCompile(`\n{3,}`)
)

// htmlToText is a small fallback used when a campaign has no text body.
func htmlToText(s string) string {
	s = htmlBreakRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
