package bulkmail

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
)

// BindJS exposes the Go service to an app's JS hooks as the $bulkmail global. Add it to the jsvm OnInit (next to
// the sandbox's, under pocker):
//
//	jsvm.MustRegister(app, jsvm.Config{OnInit: func(vm *goja.Runtime) { bulkmail.BindJS(vm, app) }})
//
//	$bulkmail.start(id, [limit]), .pause(id), .resume(id), .cancel(id) -> campaign record
//	$bulkmail.test(id, [to]), .preview(id, [email])           -> preview: {subject, html, text, to}
//	$bulkmail.count({collection, filter, email_field} | {sql}) -> {count, suppressed, sample}
//	$bulkmail.suppress(email, [reason]), .unsuppress(email), .isSuppressed(email)
//	$bulkmail.unsubscribe(email, [campaignId])                 -> suppresses and fires OnUnsubscribe
//	$bulkmail.unsubscribeURL(email, [campaignId])
//	$bulkmail.limits()                                         -> {rate, daily, sentToday}
//
// To react to bulk mail activity from JS, use the regular record hooks on its collections, for example
// onRecordAfterCreateSuccess(e => …, "_bulkmail_suppressions") or onRecordAfterUpdateSuccess(…, "_bulkmail_sends").
//
// The SQL audience (and SQL vars) runs read-only, but it can read any table of the app; a sandboxed multitenant
// host only exposes $bulkmail to tenants it trusts with their own data, which is every tenant: it's their app.
func BindJS(vm *goja.Runtime, app core.App) {
	obj := vm.NewObject()
	get := func() *BulkMail {
		bm := For(app)
		if bm == nil {
			panic(vm.NewGoError(errors.New("bulkmail isn't registered on this app")))
		}
		return bm
	}
	must := func(v any, err error) any {
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return v
	}
	str := func(call goja.FunctionCall, i int) string {
		if v := call.Argument(i); !goja.IsUndefined(v) && !goja.IsNull(v) {
			return v.String()
		}
		return ""
	}
	action := func(fn func(*BulkMail, string) (*core.Record, error)) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(must(fn(get(), str(call, 0))))
		}
	}

	_ = obj.Set("start", func(call goja.FunctionCall) goja.Value {
		limit := 0
		if v := call.Argument(1); !goja.IsUndefined(v) && !goja.IsNull(v) {
			limit = int(v.ToInteger())
		}
		return vm.ToValue(must(get().StartLimit(str(call, 0), max(limit, 0))))
	})
	_ = obj.Set("pause", action((*BulkMail).Pause))
	_ = obj.Set("resume", action((*BulkMail).Resume))
	_ = obj.Set("cancel", action((*BulkMail).Cancel))
	_ = obj.Set("test", func(call goja.FunctionCall) goja.Value {
		must(nil, get().Test(str(call, 0), str(call, 1)))
		return goja.Undefined()
	})
	_ = obj.Set("preview", func(call goja.FunctionCall) goja.Value {
		msg, err := get().Preview(str(call, 0), str(call, 1))
		must(nil, err)
		return vm.ToValue(map[string]any{
			"to": msg.To[0].Address, "from": msg.From.String(), "subject": msg.Subject,
			"html": msg.HTML, "text": msg.Text, "headers": msg.Headers,
		})
	})
	_ = obj.Set("count", func(call goja.FunctionCall) goja.Value {
		raw, err := json.Marshal(call.Argument(0).Export())
		must(nil, err)
		a, err := ParseAudience(raw)
		must(nil, err)
		n, sup, sample, err := get().CountAudience(a, 5)
		must(nil, err)
		return vm.ToValue(map[string]any{"count": n, "suppressed": sup, "sample": sample})
	})
	_ = obj.Set("suppress", func(call goja.FunctionCall) goja.Value {
		reason := str(call, 1)
		if reason == "" {
			reason = ReasonManual
		}
		return vm.ToValue(must(get().Suppress(str(call, 0), reason, "js", "")))
	})
	_ = obj.Set("unsuppress", func(call goja.FunctionCall) goja.Value {
		must(nil, get().Unsuppress(str(call, 0)))
		return goja.Undefined()
	})
	_ = obj.Set("isSuppressed", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(must(get().IsSuppressed(str(call, 0))))
	})
	_ = obj.Set("unsubscribe", func(call goja.FunctionCall) goja.Value {
		must(nil, get().Unsubscribe(str(call, 0), str(call, 1), "js"))
		return goja.Undefined()
	})
	_ = obj.Set("unsubscribeURL", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(get().UnsubscribeURL(str(call, 0), str(call, 1)))
	})
	_ = obj.Set("limits", func(call goja.FunctionCall) goja.Value {
		rate, daily, sent, err := get().Limits()
		must(nil, err)
		return vm.ToValue(map[string]any{"rate": rate, "daily": daily, "sentToday": sent})
	})

	if err := vm.Set("$bulkmail", obj); err != nil {
		panic(fmt.Errorf("bulkmail: bind JS: %w", err))
	}
}

// LegacyJSMigration returns a PocketBase JS migration that creates the bulk mail collections exactly as this
// plugin defines them (same ids), skipping any that exist. The legacy PocketHost mothership (stock PocketBase,
// no Go) runs it so its data is already in this shape when the plugin takes over.
func LegacyJSMigration() (string, error) {
	cols, err := json.MarshalIndent(Collections(), "", "  ")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("/// <reference path=\"../pb_data/types.d.ts\" />\n")
	b.WriteString("// Generated by ext/bulkmail LegacyJSMigration(). Do not edit: change the plugin's schema.go and regenerate.\n")
	b.WriteString("migrate((app) => {\n  const collections = ")
	b.WriteString(strings.ReplaceAll(string(cols), "\n", "\n  "))
	b.WriteString("\n  for (const c of collections) {\n")
	b.WriteString("    try {\n      app.findCollectionByNameOrId(c.id)\n      continue\n    } catch (_) {}\n")
	b.WriteString("    app.save(new Collection(c))\n  }\n}, (app) => {\n")
	b.WriteString("  // never drops: the ledger and suppressions are the record of who may be mailed\n})\n")
	return b.String(), nil
}
