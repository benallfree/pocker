package jserrors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

func hooksDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pb_hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Through jsvm's OnCompile: an error a handler throws names pb_hooks/<file>:<line>:<col>, not <cwd>/pb.js.
func TestHandlerErrorPointsAtHooksFile(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("jserrors=%v", on), func(t *testing.T) {
			app, _ := tests.NewTestApp()
			defer app.Cleanup()
			dir := hooksDir(t, map[string]string{"main.pb.js": "// line 1\n" +
				"onCollectionValidate((e) => {\n" +
				"  throw new Error(\"boom\")\n" +
				"})\n"})
			cfg := jsvm.Config{HooksDir: dir}
			if on {
				cfg = Config(cfg)
			}
			if err := jsvm.Register(app, cfg); err != nil {
				t.Fatal(err)
			}
			err := app.Validate(core.NewBaseCollection("x"))
			if err == nil {
				t.Fatal("expected the hook's error")
			}
			msg := err.Error()
			named := strings.Contains(msg, "pb_hooks/main.pb.js:3:")
			if named != on || strings.Contains(msg, jsvm.ScriptPath()) == on {
				t.Fatalf("error %q (jserrors on: %v)", msg, on)
			}
		})
	}
}

// Through jsvm's OnLoad: an error while a hooks file runs names the file.
func TestLoadErrorPointsAtHooksFile(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()
	dir := hooksDir(t, map[string]string{"bad.pb.js": "let a = 1\nnull.x\n"})
	var got string
	func() {
		defer func() {
			if r := recover(); r != nil {
				got = fmt.Sprint(r)
			}
		}()
		_ = jsvm.Register(app, Config(jsvm.Config{HooksDir: dir}))
	}()
	if !strings.Contains(got, "pb_hooks/bad.pb.js:2:") {
		t.Fatalf("load error %q", got)
	}
}
