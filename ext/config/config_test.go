package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/ceplugin"
)

type testCfg struct {
	Port    int           `yaml:"port"`
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
	Secret  string        `yaml:"secret"`
}

var testSection = ceplugin.NewSection("cfgtest", testCfg{Port: 1, Name: "default"}, "a test section")

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	defer ceplugin.SetSectionSource(nil)
	dir := t.TempDir()
	write(t, dir, ".env", "# secrets\nexport CFG_TEST_SECRET=\"s3 cr\\\"et\"\nCFG_TEST_PORT=8090\nCFG_TEST_NAME='quoted name'\nCFG_TEST_TRAIL=abc # comment\n")
	t.Setenv("CFG_TEST_FROM_PROCESS", "proc")
	cfgPath := write(t, dir, FileName, `
server:
  http: 0.0.0.0:$CFG_TEST_PORT
  origins: [https://a.test, "${CFG_TEST_FROM_PROCESS}.test"]
app:
  dir: ./data
  env_allow: [CFG_TEST_NAME]
cfgtest:
  port: $CFG_TEST_PORT
  name: ${CFG_TEST_NAME}-$$literal
  timeout: 30s
  secret: $CFG_TEST_SECRET
`)
	c, err := Load(Options{ConfigPath: cfgPath}) // .env found beside the config file
	if err != nil {
		t.Fatal(err)
	}
	if c.EnvPath != filepath.Join(dir, ".env") {
		t.Fatalf("env path: %q", c.EnvPath)
	}
	v, found := testSection.Get()
	if !found || v.Port != 8090 || v.Name != "quoted name-$literal" || v.Timeout != 30*time.Second || v.Secret != `s3 cr"et` {
		t.Fatalf("cfgtest: %+v %v", v, found)
	}
	s, _ := Server.Get()
	if s.HTTP != "0.0.0.0:8090" || len(s.Origins) != 2 || s.Origins[1] != "proc.test" {
		t.Fatalf("server: %+v", s)
	}
	a, _ := App.Get()
	if a.Dir != "./data" || a.Dev != nil {
		t.Fatalf("app: %+v", a)
	}
	if p, found := Provider.Get(); found || p != "" {
		t.Fatalf("provider: %q %v", p, found)
	}

	// the .env stays private: nothing reaches the process environment unless allow-listed
	for _, k := range []string{"CFG_TEST_SECRET", "CFG_TEST_PORT", "CFG_TEST_NAME", "CFG_TEST_TRAIL"} {
		if _, ok := os.LookupEnv(k); ok {
			t.Fatalf("%s leaked into the process environment", k)
		}
	}
	t.Cleanup(func() { os.Unsetenv("CFG_TEST_NAME") })
	if err := c.AllowEnv(a.EnvAllow); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CFG_TEST_NAME") != "quoted name" {
		t.Fatal("allow-listed name not exposed")
	}
	if _, ok := os.LookupEnv("CFG_TEST_SECRET"); ok {
		t.Fatal("a name not on the allow-list was exposed")
	}
}

func TestLoadErrors(t *testing.T) {
	defer ceplugin.SetSectionSource(nil)
	dir := t.TempDir()
	cases := map[string]struct{ yaml, want string }{
		"typo":        {"serverr: {http: x}\n", "unknown section(s) serverr"},
		"bad value":   {"cfgtest: {port: nope}\n", "config section cfgtest"},
		"unset var":   {"cfgtest: {secret: $CFG_TEST_NOT_SET}\n", "or the environment: CFG_TEST_NOT_SET"},
		"duplicate":   {"cfgtest: {port: 1}\ncfgtest: {port: 2}\n", "already defined"},
		"not mapping": {"- a\n- b\n", "top level must be a mapping"},
	}
	for name, tc := range cases {
		p := write(t, dir, name+".yaml", tc.yaml)
		if _, err := Load(Options{ConfigPath: p, EnvPath: write(t, dir, name+".env", "")}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
	if _, err := Load(Options{ConfigPath: filepath.Join(dir, "missing.yaml")}); err == nil {
		t.Error("an explicit config path that doesn't exist should fail")
	}
}

func TestNoFiles(t *testing.T) {
	defer ceplugin.SetSectionSource(nil)
	t.Chdir(t.TempDir())
	c, err := Load(Options{})
	if err != nil || c.Path != "" || c.EnvPath != "" {
		t.Fatalf("%+v %v", c, err)
	}
	if v, found := testSection.Get(); found || v.Port != 1 || v.Name != "default" {
		t.Fatalf("defaults: %+v %v", v, found)
	}
	// an empty section means "present, defaults"
	write(t, ".", FileName, "cfgtest:\n")
	if _, err := Load(Options{}); err != nil {
		t.Fatal(err)
	}
	if v, found := testSection.Get(); !found || v.Port != 1 {
		t.Fatalf("empty section: %+v %v", v, found)
	}
}

func TestFromArgs(t *testing.T) {
	o := FromArgs([]string{"serve", "--config", "a.yaml", "--http=1", "--env-file=b.env"})
	if o.ConfigPath != "a.yaml" || o.EnvPath != "b.env" {
		t.Fatalf("%+v", o)
	}
}

func TestCommand(t *testing.T) {
	defer ceplugin.SetSectionSource(nil)
	dir := t.TempDir()
	p := write(t, dir, FileName, "cfgtest: {port: 2}\n")
	cmd := NewCommand(func() Options { return Options{ConfigPath: p} })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"check"})
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "ok (1 sections)") {
		t.Fatalf("check: %v %q", err, out.String())
	}
	out.Reset()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "# a test section\ncfgtest:") || !strings.Contains(out.String(), "server:") {
		t.Fatalf("list: %v %q", err, out.String())
	}
}
