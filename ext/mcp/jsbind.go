package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
)

// BindJS exposes the MCP server to an app's JS hooks as the $mcp global. Add it to the jsvm OnInit:
//
//	jsvm.MustRegister(app, jsvm.Config{OnInit: func(vm *goja.Runtime) { mcp.BindJS(vm, app) }})
//
// In a pb_hooks file, at the top level (not inside a route or hook handler):
//
//	$mcp.addTool({
//	  name: "count_posts",
//	  title: "Count posts",
//	  description: "Count published posts.",
//	  inputSchema: { type: "object", properties: {} },
//	  annotations: { readOnlyHint: true },
//	  allow: "superuser",                 // default; or "auth" (any signed-in record), or ["users"] (those collections)
//	  handler: ({ args, auth, app, e }) => ({ count: app.countRecords("posts", $dbx.hashExp({ published: true })) }),
//	})
//	$mcp.instructions("count_posts counts only published posts.")
//	$mcp.tools() // -> ["count_posts", "pb_get_collection", …]
//
// Unlike routerAdd handlers, tool handlers may use the file's top-level variables. They run one at a time on the
// VM that loaded the hooks, so keep them short. Throw to return a tool error the model can read.
func BindJS(vm *goja.Runtime, app core.App) {
	obj := vm.NewObject()
	get := func() *Server {
		s := For(app)
		if s == nil {
			panic(vm.NewGoError(errors.New("mcp isn't registered on this app")))
		}
		return s
	}
	lock := vmLock(vm)

	_ = obj.Set("addTool", func(call goja.FunctionCall) goja.Value {
		def := call.Argument(0).ToObject(vm)
		handler, ok := goja.AssertFunction(def.Get("handler"))
		if !ok {
			panic(vm.NewTypeError("$mcp.addTool: handler must be a function"))
		}
		t := Tool{
			Name:        jsString(def.Get("name")),
			Title:       jsString(def.Get("title")),
			Description: jsString(def.Get("description")),
		}
		var err error
		if t.InputSchema, err = jsObject(def.Get("inputSchema")); err != nil {
			panic(vm.NewTypeError("$mcp.addTool: inputSchema: " + err.Error()))
		}
		if t.OutputSchema, err = jsObject(def.Get("outputSchema")); err != nil {
			panic(vm.NewTypeError("$mcp.addTool: outputSchema: " + err.Error()))
		}
		if a := def.Get("annotations"); a != nil && !goja.IsUndefined(a) && !goja.IsNull(a) {
			raw, err := json.Marshal(a.Export())
			if err == nil {
				t.Annotations = &Annotations{}
				err = json.Unmarshal(raw, t.Annotations)
			}
			if err != nil {
				panic(vm.NewTypeError("$mcp.addTool: annotations: " + err.Error()))
			}
		}
		if t.Allow, err = jsAllow(def.Get("allow")); err != nil {
			panic(vm.NewTypeError("$mcp.addTool: " + err.Error()))
		}
		t.Handler = func(c *Call) (result any, err error) {
			lock.Lock()
			defer lock.Unlock()
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%v", r)
				}
			}()
			ctx := vm.NewObject()
			_ = ctx.Set("args", c.Args)
			_ = ctx.Set("auth", c.Auth())
			_ = ctx.Set("app", c.App)
			_ = ctx.Set("e", c.Event)
			if c.Grant != nil {
				_ = ctx.Set("scopes", c.Grant.Scopes)
				_ = ctx.Set("clientId", c.Grant.ClientID)
			}
			v, err := handler(goja.Undefined(), ctx)
			if err != nil {
				var ex *goja.Exception
				if errors.As(err, &ex) {
					return nil, errors.New(jsErrorMessage(ex.Value()))
				}
				return nil, err
			}
			if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
				return map[string]any{}, nil
			}
			return v.Export(), nil
		}
		if err := get().AddTool(t); err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})

	_ = obj.Set("instructions", func(call goja.FunctionCall) goja.Value {
		get().AddInstructions(jsString(call.Argument(0)))
		return goja.Undefined()
	})

	_ = obj.Set("tools", func(call goja.FunctionCall) goja.Value {
		names := []string{}
		for _, t := range get().Tools() {
			names = append(names, t.Name)
		}
		return vm.ToValue(names)
	})

	if err := vm.Set("$mcp", obj); err != nil {
		panic(fmt.Errorf("mcp: bind JS: %w", err))
	}
}

var vmLocks sync.Map // *goja.Runtime -> *sync.Mutex

func vmLock(vm *goja.Runtime) *sync.Mutex {
	m, _ := vmLocks.LoadOrStore(vm, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func jsString(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func jsObject(v goja.Value) (map[string]any, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	raw, err := json.Marshal(v.Export())
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errors.New("must be an object")
	}
	return m, nil
}

func jsAllow(v goja.Value) (func(*Call) error, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return RequireSuperuser, nil
	}
	switch x := v.Export().(type) {
	case string:
		switch x {
		case "superuser":
			return RequireSuperuser, nil
		case "auth":
			return RequireAuth(), nil
		}
		return RequireAuth(x), nil
	case []any:
		cols := make([]string, 0, len(x))
		for _, c := range x {
			cols = append(cols, fmt.Sprint(c))
		}
		return RequireAuth(cols...), nil
	}
	return nil, errors.New(`allow must be "superuser", "auth", or a list of auth collection names`)
}

func jsErrorMessage(v goja.Value) string {
	if o, ok := v.(*goja.Object); ok {
		if m := o.Get("message"); m != nil && !goja.IsUndefined(m) {
			return m.String()
		}
	}
	return v.String()
}
