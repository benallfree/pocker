package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/remotes"
	"github.com/pockethost/pocketbase/ext/tokens"
)

// Scopes the server registers with the tokens plugin.
const (
	ScopeDeploy = "deploy"      // deploy, upload, delete, read
	ScopeRead   = "deploy:read" // whoami, list and download files, logs
)

// AppInstanceID is the one instance a single app serves.
const AppInstanceID = "app"

// Config configures the server half on a single app.
type Config struct {
	// Dir is the folder deploys write into (pb_hooks, pb_migrations, pb_public, node_modules live there). Default:
	// the parent of the data dir, PocketBase's default layout.
	Dir string

	// StoreDir keeps uploaded blobs and past manifests. Default: <Dir>/.pb_deploy (outside pb_data, so backups
	// skip it; not a pb_* name, so it's never deployed).
	StoreDir string

	// Prefix is the API path (default /_pocker/deploy/v1).
	Prefix string

	// Restart restarts the app after a deploy that changed hooks, migrations or packages. Default: app.Restart()
	// shortly after the response is sent (re-executes the process; not on Windows).
	Restart func(app core.App) error

	// DisableServer leaves only the client commands (e.g. a binary that deploys elsewhere but must not accept
	// deploys).
	DisableServer bool
}

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, cfg Config) *API {
	a, err := Register(app, cfg)
	if err != nil {
		panic(err)
	}
	return a
}

// Register adds the deploy API to a single app (its own files, auth by API tokens with the deploy scope or a
// superuser JWT, history in _deploys, logs from the app's _logs) and, on a *pocketbase.PocketBase, the client
// commands deploy, dev, pull and logs. It returns nil API when cfg.DisableServer.
func Register(app core.App, cfg Config) (*API, error) {
	if pb, ok := app.(*pocketbase.PocketBase); ok {
		for _, c := range Commands() {
			pb.RootCmd.AddCommand(c)
		}
	}
	if cfg.DisableServer {
		return nil, nil
	}
	if cfg.Prefix == "" {
		cfg.Prefix = PockerPrefix
	}
	if cfg.Restart == nil {
		cfg.Restart = func(app core.App) error {
			go func() {
				time.Sleep(time.Second) // let the commit response reach the client
				if err := app.Restart(); err != nil {
					app.Logger().Error("deploy: restart", "error", err)
				}
			}()
			return nil
		}
	}
	insts := &appInstances{app: app, cfg: &cfg}
	api := New(Options{
		Prefix:    cfg.Prefix,
		Backend:   &AppBackend{App: app},
		Instances: func() Instances { return insts },
		Logs:      DBLogs{App: app},
		Logger:    slog.New(slogHandler{app}),
	})
	insts.api = api
	remotes.Advertise(app, "deploy", cfg.Prefix)

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if cfg.Dir == "" {
			cfg.Dir = filepath.Dir(e.App.DataDir())
		}
		if cfg.StoreDir == "" {
			cfg.StoreDir = filepath.Join(cfg.Dir, ".pb_deploy")
		}
		api.opts.PBRoot = cfg.Dir
		api.opts.DataRoot = cfg.StoreDir
		if t := tokens.For(e.App); t != nil {
			t.RegisterScope(tokens.Scope{Name: ScopeDeploy, Description: "Deploy code (hooks, migrations, public files, packages), read files and logs",
				Paths: []string{cfg.Prefix + "/"}, Collections: []string{core.CollectionNameSuperusers}})
			t.RegisterScope(tokens.Scope{Name: ScopeRead, Description: "Read deployed files and stream logs",
				Paths: []string{cfg.Prefix + "/"}, Collections: []string{core.CollectionNameSuperusers}})
		}
		return EnsureSchema(e.App)
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		h := func(e *core.RequestEvent) error {
			ctx := context.WithValue(e.Request.Context(), requestAuthKey{}, requestAuth{record: e.Auth, token: tokens.FromRequest(e)})
			api.ServeHTTP(e.Response, e.Request.WithContext(ctx))
			return nil
		}
		// the API caps its own bodies (64 MB bundles), above PocketBase's default limit
		se.Router.Any(cfg.Prefix+"/{path...}", h).Unbind(apis.DefaultBodyLimitMiddlewareId)
		return se.Next()
	})
	return api, nil
}

type requestAuthKey struct{}

type requestAuth struct {
	record *core.Record
	token  *tokens.Info
}

// AppBackend is the default backend on a single app: one instance (the app), superusers only. A superuser JWT
// (including an impersonate token) has full access; an API token needs the deploy scope (or none), and
// deploy:read gives read-only access. History goes to the _deploys collection.
type AppBackend struct {
	App core.App
}

// Lookup implements [Backend]: every request is for the app.
func (b *AppBackend) Lookup(*http.Request) (Instance, bool) {
	return Instance{ID: AppInstanceID, Subdomain: b.App.Settings().Meta.AppName, Power: true}, true
}

// Authenticate implements [Backend] from the auth PocketBase resolved for the request.
func (b *AppBackend) Authenticate(r *http.Request, _ Instance) (Auth, error) {
	ra, _ := r.Context().Value(requestAuthKey{}).(requestAuth)
	if ra.record == nil {
		return Auth{}, &AuthError{401, "Sign in first: pocketbase login <this URL>, or send a superuser API token."}
	}
	if !ra.record.IsSuperuser() {
		return Auth{}, &AuthError{403, "Only superusers can use the deploy API."}
	}
	if ra.token == nil {
		return Auth{UserID: ra.record.Id, Scopes: ReadWrite, Via: "jwt"}, nil
	}
	a := Auth{UserID: ra.record.Id, Via: "token", TokenLabel: ra.token.Token.GetString("name")}
	switch {
	case len(ra.token.Scopes) == 0 || slices.Contains(ra.token.Scopes, ScopeDeploy):
		a.Scopes = ReadWrite
	case slices.Contains(ra.token.Scopes, ScopeRead):
		a.Scopes = []string{"read"}
	default:
		return Auth{}, &AuthError{403, "This API token has no deploy scope."}
	}
	return a, nil
}

// RecordDeploy implements [Backend]: a row in _deploys.
func (b *AppBackend) RecordDeploy(_ context.Context, rec DeployRecord) (string, error) {
	col, err := b.App.FindCachedCollectionByNameOrId(DeploysCollection)
	if err != nil {
		return "", err
	}
	r := core.NewRecord(col)
	r.Set("status", rec.Status)
	r.Set("file_count", rec.FileCount)
	r.Set("uploaded_count", rec.UploadedCount)
	r.Set("uploaded_bytes", rec.UploadedBytes)
	r.Set("total_bytes", rec.TotalBytes)
	r.Set("commit", rec.Commit)
	r.Set("message", rec.Message)
	r.Set("source", rec.Source)
	r.Set("token_label", rec.TokenLabel)
	r.Set("user", rec.User)
	r.Set("error", rec.Error)
	if err := b.App.Save(r); err != nil {
		return "", err
	}
	return r.Id, nil
}

// appInstances is the [Instances] of a single app: it can't stop itself, so files are swapped in while it runs
// (each rename is atomic), then it restarts.
type appInstances struct {
	app core.App
	cfg *Config
	api *API
	mu  sync.Mutex
}

func (a *appInstances) Running(string) bool { return true }

func (a *appInstances) InstanceDir(id string) (string, bool) {
	return a.cfg.Dir, id == AppInstanceID && a.cfg.Dir != ""
}

func (a *appInstances) WithInstanceStopped(_ context.Context, _ string, fn func(string) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return fn(a.cfg.Dir)
}

func (a *appInstances) StartInstance(context.Context, string) error { return a.cfg.Restart(a.app) }

// DBLogs is a [LogSource] over the app's own logs (the _logs table: requests and app.Logger()). Entries at error
// level and above come through as stderr. PocketBase writes logs in batches, so new lines show up a few seconds
// late.
type DBLogs struct {
	App core.App
}

type logRow struct {
	Id      string         `db:"id"`
	Created string         `db:"created"`
	Level   int            `db:"level"`
	Message string         `db:"message"`
	Data    map[string]any `db:"-"`
	RawData string         `db:"data"`
}

// Open implements [LogSource].
func (d DBLogs) Open(_ Instance, _ string, n int) ([][]byte, func() [][]byte, func()) {
	var last logRow
	var initial []logRow
	_ = d.App.LogQuery().Select("id", "created", "level", "message", "data").
		OrderBy("created DESC", "id DESC").Limit(int64(max(n, 1))).All(&initial)
	if len(initial) > 0 {
		last = initial[0]
	}
	if n == 0 {
		initial = nil
	}
	slices.Reverse(initial)
	toLines := func(rows []logRow) [][]byte {
		out := make([][]byte, 0, len(rows))
		for _, r := range rows {
			msg := formatLog(r.Message, r.RawData)
			stream := "stdout"
			if r.Level >= int(slog.LevelError) {
				stream = "stderr"
			}
			b, _ := json.Marshal(map[string]any{"message": msg, "stream": stream, "time": r.Created})
			out = append(out, b)
		}
		return out
	}
	poll := func() [][]byte {
		var rows []logRow
		q := d.App.LogQuery().Select("id", "created", "level", "message", "data").OrderBy("created ASC", "id ASC").Limit(500)
		if last.Id != "" {
			q = q.AndWhere(dbx.Or(
				dbx.NewExp("created > {:c}", dbx.Params{"c": last.Created}),
				dbx.And(dbx.NewExp("created = {:c}", dbx.Params{"c": last.Created}), dbx.NewExp("id > {:id}", dbx.Params{"id": last.Id})),
			))
		}
		if err := q.All(&rows); err != nil || len(rows) == 0 {
			return nil
		}
		last = rows[len(rows)-1]
		return toLines(rows)
	}
	return toLines(initial), poll, func() {}
}

// formatLog shortens request log entries to "GET /url → 200 (1.2 ms)"; other entries keep their data as JSON.
func formatLog(message, raw string) string {
	if raw == "" || raw == "{}" || raw == "null" {
		return message
	}
	var d map[string]any
	if json.Unmarshal([]byte(raw), &d) == nil && d["type"] == "request" {
		out := fmt.Sprintf("%v %v → %v", d["method"], d["url"], d["status"])
		if ms, ok := d["execTime"].(float64); ok {
			out += fmt.Sprintf(" (%.1f ms)", ms)
		}
		if e, ok := d["error"].(string); ok && e != "" {
			out += " " + e
		}
		return out
	}
	return message + " " + raw
}

// slogHandler sends the API's own log lines to the app logger.
type slogHandler struct{ app core.App }

func (h slogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.app.Logger().Handler().Enabled(ctx, l)
}
func (h slogHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.app.Logger().Handler().Handle(ctx, r)
}
func (h slogHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return h.app.Logger().Handler().WithAttrs(a)
}
func (h slogHandler) WithGroup(n string) slog.Handler { return h.app.Logger().Handler().WithGroup(n) }

// ---------------------------------------------------------------------------------------------------------------
// _deploys

// Deploy history on a single app.
const (
	DeploysCollection   = "_deploys"
	DeploysCollectionId = "pbc_deploys"
)

func deploysCollection() *core.Collection {
	c := core.NewBaseCollection(DeploysCollection, DeploysCollectionId)
	c.System = true
	c.ListRule, c.ViewRule, c.CreateRule, c.UpdateRule, c.DeleteRule = nil, nil, nil, nil, nil
	c.Fields.Add(
		&core.SelectField{Id: "dep_status", Name: "status", MaxSelect: 1, Values: []string{"success", "failed"}},
		&core.NumberField{Id: "dep_file_count", Name: "file_count", OnlyInt: true},
		&core.NumberField{Id: "dep_uploaded_count", Name: "uploaded_count", OnlyInt: true},
		&core.NumberField{Id: "dep_uploaded_bytes", Name: "uploaded_bytes", OnlyInt: true},
		&core.NumberField{Id: "dep_total_bytes", Name: "total_bytes", OnlyInt: true},
		&core.TextField{Id: "dep_commit", Name: "commit", Max: 100},
		&core.TextField{Id: "dep_message", Name: "message", Max: 500},
		&core.TextField{Id: "dep_source", Name: "source", Max: 100},
		&core.TextField{Id: "dep_token_label", Name: "token_label", Max: 200},
		&core.TextField{Id: "dep_user", Name: "user", Max: 100, Help: "Superuser id."},
		&core.TextField{Id: "dep_error", Name: "error", Max: 2000},
		&core.AutodateField{Id: "dep_created", Name: "created", OnCreate: true, System: true},
	)
	c.AddIndex("idx_deploys_created", false, "created", "")
	return c
}

// EnsureSchema creates the _deploys collection if it doesn't exist.
func EnsureSchema(app core.App) error {
	want := deploysCollection()
	have, err := app.FindCollectionByNameOrId(want.Id)
	if err != nil {
		if byName, nameErr := app.FindCollectionByNameOrId(want.Name); nameErr == nil {
			return fmt.Errorf("deploy: collection %q exists with id %q, expected %q", want.Name, byName.Id, want.Id)
		}
		if err := app.Save(want); err != nil {
			return fmt.Errorf("deploy: create %s: %w", want.Name, err)
		}
		return nil
	}
	var errs []error
	for _, wf := range want.Fields {
		hf := have.Fields.GetById(wf.GetId())
		if hf == nil || hf.Type() != wf.Type() || hf.GetName() != wf.GetName() {
			errs = append(errs, fmt.Errorf("field %s (%s)", wf.GetName(), wf.GetId()))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("deploy: %s doesn't match the schema: %w", want.Name, errors.Join(errs...))
	}
	return nil
}
