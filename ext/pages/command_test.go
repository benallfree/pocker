package pages

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func runPages(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "pocketbase", SilenceUsage: true, SilenceErrors: true}
	RegisterCommand(root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestPagesStarters(t *testing.T) {
	out, err := runPages(t, "pages", "starters")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"minimal", "mvp", "htmx", "daisyui", "daisyui-docs", "datastar", "auth"} {
		if !strings.Contains(out, k) {
			t.Fatalf("missing %s in\n%s", k, out)
		}
	}
	if !strings.Contains(out, "(default)") {
		t.Fatalf("no default marked:\n%s", out)
	}
}

func TestPagesCreate(t *testing.T) {
	base := t.TempDir()

	// default kit
	dir := filepath.Join(base, "site")
	out, err := runPages(t, "pages", "create", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pb_pages", "index.ejs")); err != nil {
		t.Fatalf("minimal kit not copied: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cd "+dir) {
		t.Fatalf("no next steps:\n%s", out)
	}

	// every kit copies whole, _private and migrations included, and its pb_pages builds a clean route table
	for _, s := range Starters() {
		d := filepath.Join(base, "kit-"+s.Name)
		if err := Create(d, s.Name); err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		if _, errs := Build(filepath.Join(d, "pb_pages")); len(errs) > 0 {
			t.Fatalf("%s: route table: %v", s.Name, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "kit-auth", "pb_pages", "auth", "login", "_private", "password.ejs")); err != nil {
		t.Fatalf("_private files missing: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(base, "kit-auth", "pb_migrations", "*.js")); len(m) == 0 {
		t.Fatal("auth kit's migrations missing")
	}

	// refuses an existing folder, even an empty one, and leaves it alone
	if _, err := runPages(t, "pages", "create", dir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing folder: %v", err)
	}
	empty := filepath.Join(base, "empty")
	os.Mkdir(empty, 0o755)
	if err := Create(empty, ""); err == nil {
		t.Fatal("created into an existing (empty) folder")
	}

	// unknown kit names the real ones
	if _, err := runPages(t, "pages", "create", filepath.Join(base, "x"), "--starter", "nope"); err == nil || !strings.Contains(err.Error(), "daisyui-docs") {
		t.Fatalf("unknown kit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "x")); err == nil {
		t.Fatal("unknown kit still created the folder")
	}
}
