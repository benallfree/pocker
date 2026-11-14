// Package conformance is the shared test every PocketOne plugin runs. It checks what any [pluginkit.Plugin] must get
// right whatever it does: it registers and runs on a fresh app and shuts down cleanly, it leaves PocketBase's own
// collections alone and names its own `_<name>` or `_<name>_…`, and one plugin value can serve many apps at once (as under pocker)
// without one app's registration reaching another.
//
// A plugin's test file calls it once:
//
//	func TestConformance(t *testing.T) {
//		conformance.Run(t, func() pluginkit.Plugin { return myplugin.New(myplugin.Config{}) })
//	}
//
// Run it with -race: the many-apps check registers the same plugin value on several apps concurrently. Feature tests
// stay in the plugin's own test files; this is only the minimum.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations" // PocketBase's own system migrations
	"github.com/pockethost/pocketone/ext/pluginkit"
)

// Options adjust Run for one plugin.
type Options struct {
	// Layers are the settings layers passed to Register (default: none, the plugin's own defaults).
	Layers pluginkit.Config

	// AllowCollections lists collections the plugin may create without the `_<name>_` prefix, each with a reason
	// a reviewer can check (e.g. a collection the app's own users are meant to see).
	AllowCollections map[string]string

	// Apps is how many apps the many-apps check registers the plugin on (default 3).
	Apps int

	// GoroutineSlack is how many goroutines may outlive the app (default 0). Raise it only with a comment saying
	// which goroutine stays and why it's harmless.
	GoroutineSlack int
}

// TB is the part of testing.T the checks use, so they can be tested against a recorder.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Fatal(args ...any)
	Failed() bool
	TempDir() string
}

// Run runs every check as a subtest. newPlugin must return a fresh plugin value each call.
func Run(t *testing.T, newPlugin func() pluginkit.Plugin, opts ...Options) {
	t.Helper()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	name := newPlugin().Name()
	if name == "" || strings.ContainsAny(name, " _/.") || strings.ToLower(name) != name {
		t.Fatalf("Name() = %q: want a non-empty lowercase key with no spaces, dots, slashes or underscores", name)
	}

	t.Run("Lifecycle", func(t *testing.T) { CheckLifecycle(t, newPlugin(), o) })
	t.Run("Collections", func(t *testing.T) { CheckCollections(t, newPlugin(), o) })
	t.Run("ManyApps", func(t *testing.T) { CheckManyApps(t, newPlugin(), withApps(o)) })
}

func withApps(o Options) Options {
	if o.Apps <= 0 {
		o.Apps = 3
	}
	return o
}

// CheckLifecycle: Register, bootstrap, migrations, serve hooks and terminate all succeed, and nothing the plugin
// started outlives the app (and, for a [pluginkit.Closer], its Close).
func CheckLifecycle(t TB, p pluginkit.Plugin, o Options) {
	before := goroutines()
	app := newApp(t)
	if err := start(app, p, o.Layers); err != nil {
		t.Fatal(err)
	}
	if err := stop(app); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if c, ok := p.(pluginkit.Closer); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := c.Close(ctx)
		cancel()
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	if leaked := waitGoroutines(before, o.GoroutineSlack, 2*time.Second); len(leaked) > 0 {
		t.Errorf("%d goroutine(s) outlived the app (stop them in OnTerminate, or in Close for shared ones):\n\n%s", len(leaked),
			strings.Join(leaked, "\n\n"))
	}
}

// CheckCollections: PocketBase's own collections are unchanged, and every collection the plugin adds is named
// `_<name>` or `_<name>_…` (or allowed with a reason).
func CheckCollections(t TB, p pluginkit.Plugin, o Options) {
	base := newApp(t)
	if err := start(base, nil, nil); err != nil {
		t.Fatal(err)
	}
	want := snapshot(t, base)
	_ = stop(base)

	app := newApp(t)
	if err := start(app, p, o.Layers); err != nil {
		t.Fatal(err)
	}
	defer stop(app)
	got := snapshot(t, app)

	for name, def := range want {
		if got[name] == "" {
			t.Errorf("collection %q was removed", name)
		} else if got[name] != def {
			t.Errorf("collection %q was changed; plugins must not alter PocketBase's own collections", name)
		}
	}
	prefix := "_" + p.Name() + "_"
	var added []string
	for name := range got {
		if _, ok := want[name]; !ok {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	for _, name := range added {
		if name == "_"+p.Name() || strings.HasPrefix(name, prefix) {
			continue
		}
		if reason, ok := o.AllowCollections[name]; ok && reason != "" {
			continue
		}
		t.Errorf("collection %q: plugin collections must be named _%s or %s… (or listed in AllowCollections with a reason)",
			name, p.Name(), prefix)
	}
}

// CheckManyApps: one plugin value registers on several apps, as under pocker. Registering on one app must add
// nothing to any other app's hooks; registering concurrently must work (run with -race); and every app then runs.
func CheckManyApps(t TB, p pluginkit.Plugin, o Options) {
	o = withApps(o)
	apps := make([]*core.BaseApp, o.Apps)
	for i := range apps {
		apps[i] = newApp(t)
	}
	for i, app := range apps {
		before := allHookCounts(apps)
		if err := p.Register(app, o.Layers); err != nil {
			t.Fatalf("Register on app %d: %v", i, err)
		}
		after := allHookCounts(apps)
		for j := range apps {
			if j == i {
				continue
			}
			for k, v := range after[j] {
				if v != before[j][k] {
					t.Errorf("registering on app %d added %d handler(s) to app %d's %s", i, v-before[j][k], j, k)
				}
			}
		}
	}

	more := make([]*core.BaseApp, o.Apps)
	errs := make([]error, len(more))
	var wg sync.WaitGroup
	for i := range more {
		more[i] = newApp(t)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = p.Register(more[i], o.Layers)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Register on app %d: %v", i, err)
		}
	}
	if t.Failed() {
		return
	}
	for i, app := range append(apps, more...) {
		if err := run(app); err != nil {
			t.Errorf("app %d: %v", i, err)
		}
	}
	for _, app := range append(apps, more...) {
		_ = stop(app)
	}
}

func allHookCounts(apps []*core.BaseApp) []map[string]int {
	out := make([]map[string]int, len(apps))
	for i, a := range apps {
		out[i] = hookCounts(a)
	}
	return out
}

var appSeq atomic.Int64

// newApp is a fresh app with its own host id ([pluginkit.AppID]), as pocker gives each instance.
func newApp(t TB) *core.BaseApp {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), EncryptionEnv: "", QueryTimeout: 10 * time.Second})
	pluginkit.SetAppID(app, fmt.Sprintf("app%d", appSeq.Add(1)))
	return app
}

// start registers p (if any) and runs the app up to serving.
func start(app *core.BaseApp, p pluginkit.Plugin, layers pluginkit.Config) error {
	if p != nil {
		if err := p.Register(app, layers); err != nil {
			return fmt.Errorf("Register: %w", err)
		}
	}
	return run(app)
}

// run bootstraps the app, applies every migration and triggers OnServe with a real router (no listener).
func run(app *core.BaseApp) error {
	if err := app.Bootstrap(); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	r, err := apis.NewRouter(app)
	if err != nil {
		return fmt.Errorf("router: %w", err)
	}
	se := &core.ServeEvent{App: app, Router: r, Server: &http.Server{Addr: "127.0.0.1:0"}}
	if err := app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		_, err := e.Router.BuildMux()
		return err
	}); err != nil {
		return fmt.Errorf("serve hooks: %w", err)
	}
	return nil
}

func stop(app *core.BaseApp) error {
	err := app.OnTerminate().Trigger(&core.TerminateEvent{App: app}, func(e *core.TerminateEvent) error {
		return e.App.ResetBootstrapState()
	})
	return err
}

// snapshot is every collection's definition, minus timestamps.
func snapshot(t TB, app core.App) map[string]string {
	t.Helper()
	cols, err := app.FindAllCollections()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(cols))
	for _, c := range cols {
		var m map[string]any
		b, _ := json.Marshal(c)
		_ = json.Unmarshal(b, &m)
		delete(m, "created")
		delete(m, "updated")
		b, _ = json.Marshal(m)
		out[c.Name] = string(b)
	}
	return out
}

func hookCounts(app core.App) map[string]int {
	return map[string]int{
		"OnBootstrap":              app.OnBootstrap().Length(),
		"OnServe":                  app.OnServe().Length(),
		"OnTerminate":              app.OnTerminate().Length(),
		"OnBackupCreate":           app.OnBackupCreate().Length(),
		"OnBackupRestore":          app.OnBackupRestore().Length(),
		"OnMailerSend":             app.OnMailerSend().Length(),
		"OnRealtimeConnectRequest": app.OnRealtimeConnectRequest().Length(),
		"OnSettingsReload":         app.OnSettingsReload().Length(),
	}
}

// goroutines returns the stacks of all goroutines, keyed by their first function frame.
func goroutines() map[string]int {
	out := map[string]int{}
	for _, g := range stacks() {
		out[topFrame(g)]++
	}
	return out
}

// waitGoroutines waits up to d for the goroutine set to return to before (plus slack) and returns the stacks of
// any that stayed.
func waitGoroutines(before map[string]int, slack int, d time.Duration) []string {
	deadline := time.Now().Add(d)
	for {
		var extra []string
		seen := map[string]int{}
		for _, g := range stacks() {
			top := topFrame(g)
			seen[top]++
			if seen[top] > before[top] && !ignorable(g) {
				extra = append(extra, g)
			}
		}
		if len(extra) <= slack {
			return nil
		}
		if time.Now().After(deadline) {
			return extra
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func stacks() []string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return strings.Split(strings.TrimSpace(string(buf)), "\n\n")
}

func topFrame(stack string) string {
	lines := strings.Split(stack, "\n")
	if len(lines) < 2 {
		return stack
	}
	return strings.TrimSpace(lines[1])
}

// ignorable goroutines belong to the test runner or the Go runtime, not to the plugin.
func ignorable(stack string) bool {
	top := topFrame(stack)
	for _, s := range []string{"testing.", "runtime.", "os/signal.", "conformance.stacks"} {
		if strings.HasPrefix(top, s) {
			return true
		}
	}
	// the goroutine running the check itself
	return strings.Contains(stack, "conformance.waitGoroutines")
}
