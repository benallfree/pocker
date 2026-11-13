// Package jserrors makes JS hook errors point at the .pb.js file they come from:
//
//	jsvm.MustRegister(app, jserrors.Config(jsvm.Config{...}))
//
// jsvm serializes hook handlers (fn.toString()) and recompiles each as its own program under <cwd>/pb.js
// (jsvm.ScriptPath), which also anchors relative require() calls, so its errors say pb.js:1:40. This plugin remembers
// where each handler sits in the hooks file being loaded and rewrites those positions to pb_hooks/<file>:<line>:<col>;
// errors while a hooks file itself loads name the file too.
//
// It plugs into jsvm's OnLoad and OnCompile options; PocketBase itself is unchanged.
package jserrors

import (
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
)

// Config returns base with hook errors pointing at the hooks files. Handlers already set in base.OnLoad and
// base.OnCompile still run, and their error mapping applies after this one.
func Config(base jsvm.Config) jsvm.Config {
	dir := "pb_hooks" // how errors name the hooks folder: its own name on disk, else pb_hooks (e.g. embedded)
	if base.HookFiles == nil && base.HooksDir != "" {
		dir = filepath.Base(base.HooksDir)
	}
	prevLoad, prevCompile := base.OnLoad, base.OnCompile

	base.OnLoad = func(loader *goja.Runtime, file string, content []byte) (func(), func(error) error) {
		name := filepath.ToSlash(filepath.Join(dir, file))
		setLoadingFile(loader, name, string(content))
		done, mapErr := func() {}, func(err error) error { return wholeFile(name).remap(err) }
		if prevLoad != nil {
			pd, pm := prevLoad(loader, file, content)
			done, mapErr = chainDone(pd, done), chainMap(mapErr, pm)
		}
		return func() { done(); clearLoadingFile(loader) }, mapErr
	}
	base.OnCompile = func(loader *goja.Runtime, src string, prefix int) func(error) error {
		mapErr := locateHandler(loader, src, prefix).remap
		if prevCompile != nil {
			mapErr = chainMap(mapErr, prevCompile(loader, src, prefix))
		}
		return mapErr
	}
	return base
}

func chainDone(a, b func()) func() {
	if a == nil {
		return b
	}
	return func() { b(); a() }
}

func chainMap(first, then func(error) error) func(error) error {
	if then == nil {
		return first
	}
	return func(err error) error { return then(first(err)) }
}

// loadingFile is the hooks file a loader VM is running, so handlers registered from it can be located.
type loadingFile struct {
	name    string // e.g. pb_hooks/main.pb.js
	content string
	cursor  int // where the last handler was found; handlers usually register in source order
}

var loadingFiles sync.Map // *goja.Runtime (loader) -> *loadingFile

func setLoadingFile(loader *goja.Runtime, name, content string) {
	loadingFiles.Store(loader, &loadingFile{name: name, content: content})
}

func clearLoadingFile(loader *goja.Runtime) { loadingFiles.Delete(loader) }

// hookSource maps positions in a recompiled program back to the hooks file.
type hookSource struct {
	file      string
	line, col int // where the handler starts in the file (1-based)
	prefix    int // characters the wrapper adds before the handler on the program's first line
}

// locateHandler finds src (a handler's source) in the file loader is running. Nil when unknown (e.g. a handler
// built from a string, or registered outside a hooks file).
func locateHandler(loader *goja.Runtime, src string, prefix int) *hookSource {
	if loader == nil || src == "" {
		return nil
	}
	v, ok := loadingFiles.Load(loader)
	if !ok {
		return nil
	}
	f := v.(*loadingFile)
	at := -1
	if f.cursor < len(f.content) {
		if i := strings.Index(f.content[f.cursor:], src); i >= 0 {
			at = f.cursor + i
		}
	}
	if at < 0 {
		at = strings.Index(f.content, src)
	}
	if at < 0 {
		return &hookSource{file: f.name} // the file is still worth naming
	}
	f.cursor = at + len(src)
	before := f.content[:at]
	line := strings.Count(before, "\n") + 1
	col := at - (strings.LastIndex(before, "\n") + 1) + 1
	return &hookSource{file: f.name, line: line, col: col, prefix: prefix}
}

// wholeFile maps a top-level script (the program is the file itself).
func wholeFile(name string) *hookSource { return &hookSource{file: name, line: 1, col: 1} }

// jsvm.ScriptPath depends on the working directory, so the pattern is built on first use.
var (
	posPatternOnce sync.Once
	posPattern     *regexp.Regexp
)

func scriptPosPattern() *regexp.Regexp {
	posPatternOnce.Do(func() {
		posPattern = regexp.MustCompile(regexp.QuoteMeta(jsvm.ScriptPath()) + `:(\d+):(\d+)(?:\(\d+\))?`)
	})
	return posPattern
}

func (s *hookSource) mapPos(line, col int) (int, int) {
	if s.line == 0 {
		return 0, 0
	}
	if line == 1 {
		c := s.col + col - (s.prefix + 1)
		if c < s.col {
			c = s.col // inside the wrapper
		}
		return s.line, c
	}
	return s.line + line - 1, col
}

func (s *hookSource) rewrite(text string) string {
	return scriptPosPattern().ReplaceAllStringFunc(text, func(m string) string {
		sub := scriptPosPattern().FindStringSubmatch(m)
		line, _ := strconv.Atoi(sub[1])
		col, _ := strconv.Atoi(sub[2])
		l, c := s.mapPos(line, col)
		if l == 0 {
			return s.file
		}
		return s.file + ":" + strconv.Itoa(l) + ":" + strconv.Itoa(c)
	})
}

// sourcedError is an error whose text has the hooks file positions; it unwraps to the original.
type sourcedError struct {
	err error
	msg string
}

func (e *sourcedError) Error() string { return e.msg }
func (e *sourcedError) Unwrap() error { return e.err }

// remap returns err with its pb.js positions pointing at the hooks file. Errors without them are returned as is.
func (s *hookSource) remap(err error) error {
	if s == nil || err == nil {
		return err
	}
	var ex *goja.Exception
	if !errors.As(err, &ex) {
		return err
	}
	msg := err.Error()
	if out := s.rewrite(msg); out != msg {
		return &sourcedError{err: err, msg: out}
	}
	return err
}
