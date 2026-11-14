// Package config is PocketOne's config file: pocketone.yaml, with secrets in a .env beside it.
//
// Each plugin owns one top-level section, declared with pluginkit.NewSection (a typed key the owner exports); a
// plugin reads a section only through its owner's key. This package finds and parses the files, expands $VARS,
// checks that every top-level key belongs to a linked plugin and that every section decodes, and hands sections to
// their keys (pluginkit.SetSectionSource).
//
// The .env stays private to the process: it is used only to expand $VAR references in pocketone.yaml. It is never
// written to the process environment, so JS hooks ($os.getenv) and instances don't see it, except names an operator
// lists in app.env_allow (single-app mode).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pockethost/pocketone/ext/pluginkit"
	"gopkg.in/yaml.v3"
)

// FileName is the config file looked for by default.
const FileName = "pocketone.yaml"

// Options says where the files are. Empty paths are looked up: pocketone.yaml in the working directory, then next
// to the executable; .env beside the config file (or in the working directory when there is no config file).
type Options struct {
	ConfigPath string
	EnvPath    string
}

// Config is a loaded config file.
type Config struct {
	Path    string // "" = no config file (defaults only)
	EnvPath string // "" = no .env

	sections map[string]*yaml.Node
	env      map[string]string
}

var _ pluginkit.SectionSource = (*Config)(nil)

// FromArgs reads --config and --env-file from command line arguments (before the full flag parse, which needs the
// config's values as defaults).
func FromArgs(args []string) Options {
	var o Options
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, f := range []struct {
			name string
			dst  *string
		}{{"--config", &o.ConfigPath}, {"--env-file", &o.EnvPath}} {
			switch {
			case a == f.name && i+1 < len(args):
				*f.dst = args[i+1]
				i++
			case strings.HasPrefix(a, f.name+"="):
				*f.dst = strings.TrimPrefix(a, f.name+"=")
			}
		}
	}
	return o
}

// Load finds, reads and checks the files, then installs them as the section source.
func Load(o Options) (*Config, error) {
	c, err := Read(o)
	if err != nil {
		return nil, err
	}
	if err := c.Check(); err != nil {
		return nil, err
	}
	pluginkit.SetSectionSource(c)
	return c, nil
}

// Read finds and parses the files without checking sections or installing them.
func Read(o Options) (*Config, error) {
	c := &Config{sections: map[string]*yaml.Node{}, env: map[string]string{}}
	c.Path = o.ConfigPath
	if c.Path == "" {
		c.Path = lookup(FileName)
	} else if _, err := os.Stat(c.Path); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c.EnvPath = o.EnvPath
	if c.EnvPath == "" {
		dir := "."
		if c.Path != "" {
			dir = filepath.Dir(c.Path)
		}
		if p := filepath.Join(dir, ".env"); exists(p) {
			c.EnvPath = p
		}
	}
	if c.EnvPath != "" {
		env, err := parseDotenv(c.EnvPath)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		c.env = env
	}
	if c.Path == "" {
		return c, nil
	}
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", c.Path, err)
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind == 0 || (root.Kind == yaml.ScalarNode && root.Tag == "!!null") { // empty file
		return c, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: the top level must be a mapping of sections", c.Path)
	}
	var missing []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i].Value, root.Content[i+1]
		if _, dup := c.sections[k]; dup {
			return nil, fmt.Errorf("%s:%d: section %q is already defined", c.Path, root.Content[i].Line, k)
		}
		c.expand(v, &missing)
		c.sections[k] = v
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("%s: not set in %s or the environment: %s", c.Path, envName(c.EnvPath), strings.Join(dedupe(missing), ", "))
	}
	return c, nil
}

// Check fails on top-level keys no linked plugin owns (typos) and on sections that don't decode.
func (c *Config) Check() error {
	var unknown []string
	for k := range c.sections {
		if !pluginkit.IsSection(k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		var known []string
		for _, s := range pluginkit.Sections() {
			known = append(known, s.Name)
		}
		return fmt.Errorf("%s: unknown section(s) %s (sections in this binary: %s)", c.Path, strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	if err := pluginkit.CheckSections(c); err != nil {
		return fmt.Errorf("%s: %w", c.Path, err)
	}
	return nil
}

// DecodeSection implements pluginkit.SectionSource.
func (c *Config) DecodeSection(name string, out any) (bool, error) {
	n, ok := c.sections[name]
	if !ok {
		return false, nil
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return true, nil // `name:` with nothing after it: present, all defaults
	}
	return true, n.Decode(out)
}

// Has reports whether the file has a top-level section.
func (c *Config) Has(name string) bool {
	_, ok := c.sections[name]
	return ok
}

// AllowEnv copies the named .env values into the process environment, where the app (and its JS hooks) can read
// them: the explicit allow-list for single-app mode (app.env_allow). Names not in the .env are skipped.
func (c *Config) AllowEnv(names []string) error {
	for _, n := range names {
		if v, ok := c.env[n]; ok {
			if err := os.Setenv(n, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// expand replaces $VAR and ${VAR} in every scalar value under n ($$ is a literal $), from the .env, then the process
// environment. Names that are set nowhere are collected in missing.
func (c *Config) expand(n *yaml.Node, missing *[]string) {
	switch n.Kind {
	case yaml.ScalarNode:
		if !strings.Contains(n.Value, "$") {
			return
		}
		v := expandVars(n.Value, func(name string) (string, bool) {
			if v, ok := c.env[name]; ok {
				return v, true
			}
			return os.LookupEnv(name)
		}, missing)
		if v != n.Value {
			n.Value = v
			if n.Style == 0 {
				n.Tag = "" // a plain value is typed again after expansion ("$PORT" → 8090)
			}
		}
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			c.expand(n.Content[i], missing)
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, ch := range n.Content {
			c.expand(ch, missing)
		}
	}
}

func expandVars(s string, get func(string) (string, bool), missing *[]string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		if s[i+1] == '$' {
			b.WriteByte('$')
			i++
			continue
		}
		name, end := "", i+1
		if s[i+1] == '{' {
			if j := strings.IndexByte(s[i+2:], '}'); j >= 0 {
				name, end = s[i+2:i+2+j], i+3+j
			}
		} else {
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= 'a' && s[j] <= 'z' || j > i+1 && s[j] >= '0' && s[j] <= '9') {
				j++
			}
			name, end = s[i+1:j], j
		}
		if name == "" {
			b.WriteByte('$')
			continue
		}
		if v, ok := get(name); ok {
			b.WriteString(v)
		} else {
			*missing = append(*missing, name)
		}
		i = end - 1
	}
	return b.String()
}

func lookup(name string) string {
	if exists(name) {
		return name
	}
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), name); exists(p) {
			return p
		}
	}
	return ""
}

func exists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func envName(p string) string {
	if p == "" {
		return "the .env"
	}
	return p
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// ErrNoProvider is returned when the config names a runtime provider this binary doesn't link.
var ErrNoProvider = errors.New("this binary links no runtime provider")

// ResolvePath makes a relative path from the config file absolute, relative to the file's folder (so a config file
// works from any working directory, and paths handed to other processes, such as docker bind mounts, are absolute).
// Absolute and empty paths, and paths without a config file, are returned unchanged.
func (c *Config) ResolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) || c.Path == "" {
		return p
	}
	joined := filepath.Join(filepath.Dir(c.Path), p)
	if abs, err := filepath.Abs(joined); err == nil {
		return abs
	}
	return joined
}
