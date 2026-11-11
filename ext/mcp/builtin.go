package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// BuiltinGroups are the groups of pb_* tools.
var BuiltinGroups = []string{"info", "collections", "records", "settings", "logs", "backups"}

// The built-in tools call the app directly, so they bypass collection API rules; every one requires a superuser.
// Ported from pbjsmcp (github.com/benallfree/pbjsmcp) with the same names and safety rules.
func builtinTools(groups []string, readOnly bool) []Tool {
	if len(groups) == 0 {
		groups = BuiltinGroups
	}
	out := []Tool{}
	for _, b := range builtins {
		if !slices.Contains(groups, b.group) || (readOnly && !b.tool.Annotations.ReadOnly()) {
			continue
		}
		t := b.tool
		t.Allow = RequireSuperuser
		out = append(out, t)
	}
	return out
}

type builtin struct {
	group string
	tool  Tool
}

// ---------------------------------------------------------------- helpers

func argStr(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func argInt(args map[string]any, key string, def, lo, hi int) int {
	n := def
	switch v := args[key].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case int64:
		n = int(v)
	case string:
		if p, err := strconv.Atoi(v); err == nil {
			n = p
		}
	}
	return max(lo, min(hi, n))
}

func argObject(args map[string]any, key string) (map[string]any, error) {
	switch v := args[key].(type) {
	case map[string]any:
		return v, nil
	case nil:
		return nil, fmt.Errorf("%q is required", key)
	default:
		return nil, fmt.Errorf("%q must be an object", key)
	}
}

func splitList(v any) []string {
	out := []string{}
	switch x := v.(type) {
	case []any:
		for _, i := range x {
			if s := strings.TrimSpace(fmt.Sprint(i)); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.Split(x, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func toMap(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(raw, &out)
}

func findCollection(app core.App, nameOrID string) (*core.Collection, error) {
	c, err := app.FindCollectionByNameOrId(nameOrID)
	if err != nil {
		return nil, fmt.Errorf("collection %q not found; use pb_list_collections to see what exists", nameOrID)
	}
	return c, nil
}

func findRecord(app core.App, collection, id string) (*core.Record, error) {
	c, err := findCollection(app, collection)
	if err != nil {
		return nil, err
	}
	r, err := app.FindRecordById(c, id)
	if err != nil {
		return nil, fmt.Errorf("record %q not found in %q", id, c.Name)
	}
	return r, nil
}

// exportRecord is the record's public JSON (hidden fields like passwords and token keys are left out).
func exportRecord(app core.App, r *core.Record, expand []string) (any, error) {
	r.IgnoreEmailVisibility(true)
	if len(expand) > 0 {
		app.ExpandRecord(r, expand, nil)
	}
	return toMap(r)
}

func collectionSummary(c *core.Collection) map[string]any {
	fields := make([]string, 0, len(c.Fields))
	for _, f := range c.Fields {
		fields = append(fields, f.GetName()+":"+f.Type())
	}
	return map[string]any{"id": c.Id, "name": c.Name, "type": c.Type, "system": c.System, "fields": fields}
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var collectionArg = map[string]any{"type": "string", "description": "Collection name or id"}

// ---------------------------------------------------------------- tools

var builtins = []builtin{
	// ------------------------------------------------------------ info
	{"info", Tool{
		Name:        "pb_server_info",
		Title:       "Server info",
		Description: "PocketBase app name/URL, dev mode, collection counts, and which auth record this connection uses.",
		Annotations: Read,
		Handler: func(c *Call) (any, error) {
			meta := c.App.Settings().Meta
			all, err := c.App.FindAllCollections()
			if err != nil {
				return nil, err
			}
			user := 0
			for _, col := range all {
				if !col.System {
					user++
				}
			}
			info := map[string]any{
				"appName":     meta.AppName,
				"appURL":      meta.AppURL,
				"isDev":       c.App.IsDev(),
				"collections": map[string]any{"total": len(all), "user": user},
			}
			if a := c.Auth(); a != nil {
				info["auth"] = map[string]any{"id": a.Id, "collection": a.Collection().Name, "email": a.Email()}
			}
			if c.Grant != nil {
				info["scopes"] = c.Grant.Scopes
			}
			return info, nil
		},
	}},

	// ------------------------------------------------------------ collections
	{"collections", Tool{
		Name:        "pb_list_collections",
		Title:       "List collections",
		Description: "List collections with id, type and fields (name:type).",
		Annotations: Read,
		InputSchema: obj(map[string]any{
			"type":          map[string]any{"type": "string", "enum": []string{"base", "auth", "view"}},
			"includeSystem": map[string]any{"type": "boolean", "default": false},
		}),
		Handler: func(c *Call) (any, error) {
			var types []string
			if t := argStr(c.Args, "type"); t != "" {
				types = []string{t}
			}
			list, err := c.App.FindAllCollections(types...)
			if err != nil {
				return nil, err
			}
			includeSystem, _ := c.Args["includeSystem"].(bool)
			out := []map[string]any{}
			for _, col := range list {
				if includeSystem || !col.System {
					out = append(out, collectionSummary(col))
				}
			}
			return map[string]any{"collections": out}, nil
		},
	}},
	{"collections", Tool{
		Name:        "pb_get_collection",
		Title:       "Get collection",
		Description: "Full definition of one collection: fields with options, API rules, indexes, auth options.",
		Annotations: Read,
		InputSchema: obj(map[string]any{"collection": collectionArg}, "collection"),
		Handler: func(c *Call) (any, error) {
			col, err := findCollection(c.App, argStr(c.Args, "collection"))
			if err != nil {
				return nil, err
			}
			return toMap(col)
		},
	}},
	{"collections", Tool{
		Name:  "pb_create_collection",
		Title: "Create collection",
		Description: "Create a collection. `collection` uses the same JSON shape as the PocketBase collections API " +
			"(name, type, fields, listRule/viewRule/createRule/updateRule/deleteRule, indexes). " +
			"Rules default to null (superuser only).",
		Annotations: Write,
		InputSchema: obj(map[string]any{
			"collection": map[string]any{
				"type":        "object",
				"description": `e.g. {"name":"posts","type":"base","fields":[{"name":"title","type":"text","required":true}]}`,
			},
		}, "collection"),
		Handler: func(c *Call) (any, error) {
			def, err := argObject(c.Args, "collection")
			if err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(def)
			col := &core.Collection{}
			if err := json.Unmarshal(raw, col); err != nil {
				return nil, err
			}
			if err := c.App.Save(col); err != nil {
				return nil, err
			}
			return toMap(col)
		},
	}},
	{"collections", Tool{
		Name:  "pb_update_collection",
		Title: "Update collection",
		Description: "Update a collection by merging `changes` onto it (same JSON shape as the collections API). " +
			"Careful: `fields` and `indexes` REPLACE the whole list. To keep a field, include it with its existing id " +
			"(get it from pb_get_collection first). A field left out is dropped along with its data.",
		Annotations: Destroy,
		InputSchema: obj(map[string]any{"collection": collectionArg, "changes": map[string]any{"type": "object"}}, "collection", "changes"),
		Handler: func(c *Call) (any, error) {
			col, err := findCollection(c.App, argStr(c.Args, "collection"))
			if err != nil {
				return nil, err
			}
			changes, err := argObject(c.Args, "changes")
			if err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(changes)
			if err := json.Unmarshal(raw, col); err != nil {
				return nil, err
			}
			if err := c.App.Save(col); err != nil {
				return nil, err
			}
			return toMap(col)
		},
	}},
	{"collections", Tool{
		Name:        "pb_delete_collection",
		Title:       "Delete collection",
		Description: "Delete a collection and ALL its records. `confirm` must repeat the collection name.",
		Annotations: Destroy,
		InputSchema: obj(map[string]any{"collection": collectionArg, "confirm": map[string]any{"type": "string"}}, "collection", "confirm"),
		Handler: func(c *Call) (any, error) {
			col, err := findCollection(c.App, argStr(c.Args, "collection"))
			if err != nil {
				return nil, err
			}
			if argStr(c.Args, "confirm") != col.Name {
				return nil, fmt.Errorf("confirm must be %q", col.Name)
			}
			if err := c.App.Delete(col); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": col.Name}, nil
		},
	}},
	{"collections", Tool{
		Name:  "pb_import_collections",
		Title: "Import collections",
		Description: "Create or update several collections in one transaction (matched by id, then name). " +
			"Existing collections not in the list are left alone.",
		Annotations: Write,
		InputSchema: obj(map[string]any{
			"collections": map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "minItems": 1},
		}, "collections"),
		Handler: func(c *Call) (any, error) {
			items, _ := c.Args["collections"].([]any)
			if len(items) == 0 {
				return nil, errors.New(`"collections" must be a non-empty array`)
			}
			list := make([]map[string]any, 0, len(items))
			names := []string{}
			for _, it := range items {
				m, ok := it.(map[string]any)
				if !ok {
					return nil, errors.New(`every item of "collections" must be an object`)
				}
				list = append(list, m)
				if n := argStr(m, "name"); n != "" {
					names = append(names, n)
				} else {
					names = append(names, argStr(m, "id"))
				}
			}
			if err := c.App.ImportCollections(list, false); err != nil {
				return nil, err
			}
			return map[string]any{"imported": names}, nil
		},
	}},

	// ------------------------------------------------------------ records
	{"records", Tool{
		Name:  "pb_list_records",
		Title: "List records",
		Description: "List records. `filter` and `sort` use PocketBase syntax, e.g. filter \"status = {:s} && created > '2026-01-01'\" " +
			"with params {\"s\":\"active\"}, sort \"-created\". Hidden fields (passwords, tokens) are never returned.",
		Annotations: Read,
		InputSchema: obj(map[string]any{
			"collection": collectionArg,
			"filter":     map[string]any{"type": "string"},
			"params":     map[string]any{"type": "object", "description": "Values for {:name} placeholders in filter"},
			"sort":       map[string]any{"type": "string", "default": "-created"},
			"page":       map[string]any{"type": "integer", "minimum": 1, "default": 1},
			"perPage":    map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 30},
			"expand":     map[string]any{"type": "string", "description": "Comma-separated relation fields"},
		}, "collection"),
		Handler: func(c *Call) (any, error) {
			col, err := findCollection(c.App, argStr(c.Args, "collection"))
			if err != nil {
				return nil, err
			}
			page := argInt(c.Args, "page", 1, 1, 1e9)
			perPage := argInt(c.Args, "perPage", 30, 1, 500)
			sortBy, given := c.Args["sort"].(string)
			if !given && col.Fields.GetByName("created") != nil {
				sortBy = "-created"
			}
			params := dbx.Params{}
			if p, ok := c.Args["params"].(map[string]any); ok {
				for k, v := range p {
					params[k] = v
				}
			}
			// one extra row tells whether there's another page
			rows, err := c.App.FindRecordsByFilter(col, argStr(c.Args, "filter"), sortBy, perPage+1, (page-1)*perPage, params)
			if err != nil {
				return nil, err
			}
			expand := splitList(c.Args["expand"])
			items := []any{}
			for i, r := range rows {
				if i == perPage {
					break
				}
				m, err := exportRecord(c.App, r, expand)
				if err != nil {
					return nil, err
				}
				items = append(items, m)
			}
			return map[string]any{"page": page, "perPage": perPage, "hasMore": len(rows) > perPage, "items": items}, nil
		},
	}},
	{"records", Tool{
		Name:        "pb_get_record",
		Title:       "Get record",
		Description: "Get one record by id.",
		Annotations: Read,
		InputSchema: obj(map[string]any{"collection": collectionArg, "id": map[string]any{"type": "string"}, "expand": map[string]any{"type": "string"}}, "collection", "id"),
		Handler: func(c *Call) (any, error) {
			r, err := findRecord(c.App, argStr(c.Args, "collection"), argStr(c.Args, "id"))
			if err != nil {
				return nil, err
			}
			return exportRecord(c.App, r, splitList(c.Args["expand"]))
		},
	}},
	{"records", Tool{
		Name:  "pb_create_record",
		Title: "Create record",
		Description: "Create a record. For auth collections include \"password\" (\"passwordConfirm\" is not needed). " +
			"File fields are not supported here.",
		Annotations: Write,
		InputSchema: obj(map[string]any{"collection": collectionArg, "data": map[string]any{"type": "object"}}, "collection", "data"),
		Handler: func(c *Call) (any, error) {
			col, err := findCollection(c.App, argStr(c.Args, "collection"))
			if err != nil {
				return nil, err
			}
			data, err := argObject(c.Args, "data")
			if err != nil {
				return nil, err
			}
			r := core.NewRecord(col)
			r.Load(data)
			if err := c.App.Save(r); err != nil {
				return nil, err
			}
			return exportRecord(c.App, r, nil)
		},
	}},
	{"records", Tool{
		Name:        "pb_update_record",
		Title:       "Update record",
		Description: "Update fields on one record. Only the keys in `data` change.",
		Annotations: Write,
		InputSchema: obj(map[string]any{"collection": collectionArg, "id": map[string]any{"type": "string"}, "data": map[string]any{"type": "object"}}, "collection", "id", "data"),
		Handler: func(c *Call) (any, error) {
			r, err := findRecord(c.App, argStr(c.Args, "collection"), argStr(c.Args, "id"))
			if err != nil {
				return nil, err
			}
			data, err := argObject(c.Args, "data")
			if err != nil {
				return nil, err
			}
			r.Load(data)
			if err := c.App.Save(r); err != nil {
				return nil, err
			}
			return exportRecord(c.App, r, nil)
		},
	}},
	{"records", Tool{
		Name:        "pb_delete_record",
		Title:       "Delete record",
		Description: "Delete one record (cascades follow the collection relation settings).",
		Annotations: Destroy,
		InputSchema: obj(map[string]any{"collection": collectionArg, "id": map[string]any{"type": "string"}}, "collection", "id"),
		Handler: func(c *Call) (any, error) {
			r, err := findRecord(c.App, argStr(c.Args, "collection"), argStr(c.Args, "id"))
			if err != nil {
				return nil, err
			}
			if err := c.App.Delete(r); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": r.Id}, nil
		},
	}},

	// ------------------------------------------------------------ settings
	{"settings", Tool{
		Name:        "pb_get_settings",
		Title:       "Get settings",
		Description: "App settings (meta, smtp, s3, backups, rate limits, logs, batch, trusted proxy). Secrets are left out.",
		Annotations: Read,
		Handler: func(c *Call) (any, error) {
			return toMap(c.App.Settings()) // PocketBase's own JSON masks the SMTP password and S3 secrets
		},
	}},
	{"settings", Tool{
		Name:  "pb_update_settings",
		Title: "Update settings",
		Description: `Merge ` + "`changes`" + ` into the app settings, e.g. {"meta":{"appName":"Acme"}} or {"smtp":{"enabled":true,"host":"..."}}. ` +
			"Nested objects merge; arrays replace.",
		Annotations: Write,
		InputSchema: obj(map[string]any{"changes": map[string]any{"type": "object"}}, "changes"),
		Handler: func(c *Call) (any, error) {
			changes, err := argObject(c.Args, "changes")
			if err != nil {
				return nil, err
			}
			s, err := c.App.Settings().Clone()
			if err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(changes)
			if err := json.Unmarshal(raw, s); err != nil {
				return nil, err
			}
			if err := c.App.Save(s); err != nil {
				return nil, err
			}
			keys := make([]string, 0, len(changes))
			for k := range changes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return map[string]any{"updated": keys}, nil
		},
	}},

	// ------------------------------------------------------------ logs
	{"logs", Tool{
		Name:        "pb_list_logs",
		Title:       "List logs",
		Description: "Recent request and app logs, newest first.",
		Annotations: Read,
		InputSchema: obj(map[string]any{
			"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
			"minLevel": map[string]any{"type": "integer", "description": "-4 debug, 0 info, 4 warn, 8 error", "default": -4},
			"search":   map[string]any{"type": "string", "description": "Substring match on the message"},
		}),
		Handler: func(c *Call) (any, error) {
			q := c.App.LogQuery().AndWhere(dbx.NewExp("level >= {:lvl}", dbx.Params{"lvl": argInt(c.Args, "minLevel", -4, -100, 100)}))
			if s := argStr(c.Args, "search"); s != "" {
				q = q.AndWhere(dbx.Like("message", s))
			}
			logs := []*core.Log{}
			if err := q.OrderBy("created DESC").Limit(int64(argInt(c.Args, "limit", 50, 1, 200))).All(&logs); err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(logs))
			for _, l := range logs {
				out = append(out, map[string]any{"id": l.Id, "created": l.Created, "level": l.Level, "message": l.Message, "data": l.Data})
			}
			return map[string]any{"logs": out}, nil
		},
	}},

	// ------------------------------------------------------------ backups
	{"backups", Tool{
		Name:        "pb_list_backups",
		Title:       "List backups",
		Description: "List backup files, newest first.",
		Annotations: Read,
		Handler: func(c *Call) (any, error) {
			fsys, err := c.App.NewBackupsFilesystem()
			if err != nil {
				return nil, err
			}
			defer fsys.Close()
			files, err := fsys.List("")
			if err != nil {
				return nil, err
			}
			sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
			out := make([]map[string]any, 0, len(files))
			for _, f := range files {
				out = append(out, map[string]any{"key": f.Key, "size": f.Size, "modified": f.ModTime.UTC().Format("2006-01-02 15:04:05.000Z")})
			}
			return map[string]any{"backups": out}, nil
		},
	}},
	{"backups", Tool{
		Name:        "pb_create_backup",
		Title:       "Create backup",
		Description: "Create a backup zip of pb_data now. Restoring is intentionally not exposed.",
		Annotations: Write,
		InputSchema: obj(map[string]any{"name": map[string]any{"type": "string", "description": "Optional, must end in .zip"}}),
		Handler: func(c *Call) (any, error) {
			name := argStr(c.Args, "name")
			if name == "" {
				name = "mcp_" + time.Now().UTC().Format("20060102150405") + ".zip"
			}
			if err := c.App.CreateBackup(c.Event.Request.Context(), name); err != nil {
				return nil, err
			}
			return map[string]any{"created": name}, nil
		},
	}},
}
