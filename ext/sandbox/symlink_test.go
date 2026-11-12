package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
)

// require() resolves symlinks before loading, so a module in an app folder reached through a symlink (macOS temp
// folders: /var -> /private/var) arrives with the real path and must still count as inside.
func TestRequireThroughSymlinkedDir(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "app", "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "app", "pb_hooks", "lib.js"), []byte(`module.exports = { name: "own lib" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(real, "app"), link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	s := &Sandbox{Dir: link}
	vm := goja.New()
	s.OnInit(vm)
	_ = vm.Set("__hooks", filepath.Join(link, "pb_hooks"))
	for _, src := range []string{`require(__hooks + "/lib.js").name`, `require(__hooks + "/lib").name`} {
		v, err := vm.RunString(src)
		if err != nil || v.String() != "own lib" {
			t.Fatalf("%s: %v %v", src, v, err)
		}
	}
	// still refused: a real path outside the app folder
	if _, err := vm.RunString(`require("` + filepath.Join(real, "outside.js") + `")`); err == nil {
		t.Fatal("required a file outside the app folder")
	}
}
