package pages

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The EJS compiler turns a template into the source of one JS function:
//
//	function (__pp, __file, __locals) { ... return __b.s }
//
// __pp carries the request helpers (ctx, include, asset, meta, slots), __file is the template's path relative to
// pb_pages (for includes and errors), __locals are an include's extra variables. The function returns the rendered
// text. Line numbers are tracked in __l so errors can name the template line.

var scriptServerRe = regexp.MustCompile(`(?s)<script\s+server\s*>(.*?)</script>`)

// templatePrelude defines the names a template can use. `with (__locals)` makes include locals top-level variables.
const templatePrelude = `var ctx = __pp.ctx, data = ctx.data, params = ctx.params, meta = ctx.meta;
var asset = function (p) { return __pp.asset(__file, p); };
var __b = { s: '' }, __l = 1;
var include = function (n, l) { return __pp.include(__file, n, l); };
var require = function (n) { return __pp.require(__file, n); };
var slot = function (n, f) {
  if (typeof f === 'function') { var s = __b.s; __b.s = ''; f(); var c = __b.s; __b.s = s; __pp.setSlot(n, c); return ''; }
  if (n === undefined || n === null) return __pp.wrapped();
  return __pp.getSlot(n);
};
var __esc = __pp.esc, __str = __pp.str;
`

// CompileEJS returns the JS function source for an EJS template.
func CompileEJS(src string) (string, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = scriptServerRe.ReplaceAllString(src, "<%$1%>")

	var body strings.Builder
	line := 1
	emitText := func(s string) {
		if s == "" {
			return
		}
		q, _ := json.Marshal(s)
		fmt.Fprintf(&body, "__b.s += %s;\n", q)
	}
	setLine := func() { fmt.Fprintf(&body, "__l = %d;\n", line) }

	i := 0
	for i < len(src) {
		open := strings.Index(src[i:], "<%")
		if open < 0 {
			emitText(src[i:])
			break
		}
		open += i
		// literal <%%
		if strings.HasPrefix(src[open:], "<%%") {
			emitText(src[i:open] + "<%")
			line += strings.Count(src[i:open], "\n")
			i = open + 3
			continue
		}

		text := src[i:open]
		tagStart := open + 2
		kind := byte(0)
		if tagStart < len(src) {
			switch src[tagStart] {
			case '=', '-', '#', '_':
				kind = src[tagStart]
				tagStart++
			}
		}
		if kind == '_' {
			// <%_ strips whitespace before the tag on its line
			text = strings.TrimRight(text, " \t")
		}
		emitText(text)
		line += strings.Count(src[i:open], "\n")

		closeRel, closeLen, trim := findClose(src[tagStart:])
		if closeRel < 0 {
			return "", fmt.Errorf("line %d: unclosed EJS tag", line)
		}
		code := src[tagStart : tagStart+closeRel]
		code = strings.ReplaceAll(code, "%%>", "%>")
		setLine()
		switch kind {
		case '=':
			fmt.Fprintf(&body, "__b.s += __esc(%s);\n", trimExpr(code))
		case '-':
			fmt.Fprintf(&body, "__b.s += __str(%s);\n", trimExpr(code))
		case '#':
			// comment
		default:
			body.WriteString(code)
			body.WriteString("\n")
		}
		line += strings.Count(code, "\n")
		i = tagStart + closeRel + closeLen
		switch trim {
		case '-':
			// -%> drops one newline right after the tag
			if strings.HasPrefix(src[i:], "\n") {
				i++
				line++
			}
		case '_':
			// _%> drops whitespace and one newline after the tag
			j := i
			for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			if j < len(src) && src[j] == '\n' {
				j++
				line++
			}
			i = j
		}
	}

	var out strings.Builder
	out.WriteString("(function (__pp, __file, __locals) {\n")
	out.WriteString(templatePrelude)
	out.WriteString("try {\nwith (__locals || {}) {\n")
	out.WriteString(body.String())
	out.WriteString("}\n} catch (e) { throw __pp.rethrow(e, __file, __l); }\nreturn __b.s;\n})")
	return out.String(), nil
}

// findClose finds the end of an EJS tag: "%>", "-%>" or "_%>" (but not "%%>"). It returns the offset of the closing
// sequence, its length and the trim mode ('-', '_' or 0).
func findClose(s string) (int, int, byte) {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 >= len(s) || s[i+1] != '>' {
			continue
		}
		if i > 0 && s[i-1] == '%' {
			continue // %%> is a literal
		}
		if i > 0 && (s[i-1] == '-' || s[i-1] == '_') {
			return i - 1, 3, s[i-1]
		}
		return i, 2, 0
	}
	return -1, 0, 0
}

// trimExpr drops whitespace and a trailing semicolon from an output tag's expression.
func trimExpr(code string) string {
	code = strings.TrimSpace(code)
	code = strings.TrimSuffix(code, ";")
	if code == "" {
		return "''"
	}
	return "(" + code + ")"
}
