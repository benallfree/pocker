package pages

import (
	_ "embed"
	"fmt"
	"path"
	"strings"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// JSX/TSX pages and TypeScript modules (SPEC 4.5). esbuild, linked into the binary, strips types and turns JSX into
// calls to __h (classic transform); the runtime in jsxruntime.js renders those calls straight to HTML strings. There
// is no virtual DOM and nothing runs in the browser: a component is a function from props to markup.

//go:embed jsxruntime.js
var jsxRuntimeSource string

var jsxRuntimeProgram = goja.MustCompile("ext/pages/jsxruntime.js", jsxRuntimeSource, false)

// tsExts are module extensions that go through esbuild.
var tsExts = map[string]api.Loader{".ts": api.LoaderTS, ".tsx": api.LoaderTSX, ".jsx": api.LoaderJSX}

// isJSX reports whether file is a JSX/TSX template.
func isJSX(file string) bool {
	e := path.Ext(file)
	return e == ".jsx" || e == ".tsx"
}

// transpile turns TypeScript/JSX source into plain CommonJS for goja. Other files are returned unchanged.
func transpile(file string, src []byte) (string, error) {
	loader, ok := tsExts[path.Ext(file)]
	if !ok {
		return string(src), nil
	}
	res := api.Transform(string(src), api.TransformOptions{
		Loader:      loader,
		Format:      api.FormatCommonJS,
		Target:      api.ES2017,
		JSX:         api.JSXTransform,
		JSXFactory:  "__h",
		JSXFragment: "__Fragment",
		Sourcefile:  file,
		Sourcemap:   api.SourceMapNone,
	})
	if len(res.Errors) > 0 {
		var b strings.Builder
		for i, m := range res.Errors {
			if i > 0 {
				b.WriteString("; ")
			}
			if m.Location != nil {
				fmt.Fprintf(&b, "%s:%d:%d: ", file, m.Location.Line, m.Location.Column)
			}
			b.WriteString(m.Text)
		}
		return "", fmt.Errorf("%s", b.String())
	}
	return string(res.Code), nil
}

// jsxRuntime returns the runtime object ({ h, Fragment, raw, toHTML }) for this VM.
func (r *request) jsxRuntime() *goja.Object {
	if v := r.cache.Get("jsx"); v != nil && !goja.IsUndefined(v) {
		return v.ToObject(r.vm)
	}
	fn, err := r.programFunc("jsxRuntime", jsxRuntimeProgram)
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	v, err := fn(goja.Undefined(), r.vm.ToValue(func(s string) string { return escapeHTML(s) }))
	if err != nil {
		panic(err)
	}
	r.cache.Set("jsx", v)
	return v.ToObject(r.vm)
}

func (r *request) jsxCall(name string, args ...goja.Value) goja.Value {
	fn, ok := goja.AssertFunction(r.jsxRuntime().Get(name))
	if !ok {
		panic(r.vm.NewGoError(fmt.Errorf("jsx runtime: no %s", name)))
	}
	v, err := fn(goja.Undefined(), args...)
	if err != nil {
		panic(err)
	}
	return v
}

// rawHTML wraps s so JSX inserts it without escaping.
func (r *request) rawHTML(s string) goja.Value { return r.jsxCall("raw", r.vm.ToValue(s)) }

// renderJSX renders a .jsx/.tsx template: its default export is called with props and the result turned to HTML.
// Props: ctx, data, params, meta, asset, include, slot, children (the inner content, for layouts), plus an include's
// locals.
func (r *request) renderJSX(file string, locals goja.Value) string {
	exports := r.load(file).ToObject(r.vm)
	comp := exports.Get("default")
	if comp == nil || goja.IsUndefined(comp) {
		comp = exports
	}
	fn, ok := goja.AssertFunction(comp)
	if !ok {
		panic(r.vm.NewGoError(fmt.Errorf("%s: the default export must be a component function", file)))
	}

	vm := r.vm
	props := vm.NewObject()
	if locals != nil && !goja.IsUndefined(locals) && !goja.IsNull(locals) {
		lo := locals.ToObject(vm)
		for _, k := range lo.Keys() {
			_ = props.Set(k, lo.Get(k))
		}
	}
	ctx := r.pp.ToObject(vm).Get("ctx").ToObject(vm)
	_ = props.Set("ctx", ctx)
	_ = props.Set("data", ctx.Get("data"))
	_ = props.Set("params", ctx.Get("params"))
	_ = props.Set("meta", ctx.Get("meta"))
	_ = props.Set("asset", func(p string) string { return r.p.Asset(path.Dir(file), p) })
	_ = props.Set("include", func(name string, l goja.Value) goja.Value {
		return r.rawHTML(r.includeFrom(file, name, l))
	})
	_ = props.Set("slot", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0)
		if goja.IsUndefined(name) || goja.IsNull(name) {
			return r.rawHTML(r.wrapped)
		}
		if len(call.Arguments) > 1 {
			r.slots[name.String()] = r.jsxCall("toHTML", call.Argument(1)).String()
			return goja.Undefined()
		}
		return r.rawHTML(r.slots[name.String()])
	})
	_ = props.Set("children", r.rawHTML(r.wrapped))

	out, err := fn(goja.Undefined(), props)
	if err != nil {
		panic(err)
	}
	return r.jsxCall("toHTML", out).String()
}

func escapeHTML(s string) string {
	if !strings.ContainsAny(s, `&<>"'`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, c := range s {
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&#34;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
