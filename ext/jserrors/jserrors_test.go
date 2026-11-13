package jserrors

import (
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
)

func TestHookSourcePositions(t *testing.T) {
	content := "// header\n" +
		"onBootstrap((e) => {\n" +
		"  e.next()\n" +
		"})\n" +
		"routerAdd(\"GET\", \"/x\", (e) => {\n" +
		"  throw new Error(\"x\")\n" +
		"})\n"
	loader := goja.New()
	setLoadingFile(loader, "pb_hooks/main.pb.js", content)
	defer clearLoadingFile(loader)

	first := "(e) => {\n  e.next()\n}"
	second := "(e) => {\n  throw new Error(\"x\")\n}"

	s1 := locateHandler(loader, first, 2)
	if s1.line != 2 || s1.col != 13 {
		t.Fatalf("first handler at %d:%d, want 2:13", s1.line, s1.col)
	}
	s2 := locateHandler(loader, second, 2)
	if s2.line != 5 || s2.col != 24 {
		t.Fatalf("second handler at %d:%d, want 5:24", s2.line, s2.col)
	}

	// run the second handler the way the binds do and check the thrown position maps to line 6
	pr := goja.MustCompile(jsvm.ScriptPath(), "{("+second+").apply(undefined, [])}", true)
	src := locateHandler(loader, second, 2)
	_, err := goja.New().RunProgram(pr)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := src.remap(err).Error()
	if !strings.Contains(msg, "pb_hooks/main.pb.js:6:9") || strings.Contains(msg, jsvm.ScriptPath()) {
		t.Fatalf("remapped error: %q", msg)
	}
}

func TestHookSourceWholeFileAndUnknown(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunScript(jsvm.ScriptPath(), "let a = 1\nnull.x\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if msg := wholeFile("pb_hooks/a.pb.js").remap(err).Error(); !strings.Contains(msg, "pb_hooks/a.pb.js:2:") {
		t.Fatalf("whole file: %q", msg)
	}

	// no loading file (e.g. a handler registered at runtime): nothing to map, the error is unchanged
	if s := locateHandler(goja.New(), "() => {}", 2); s != nil {
		t.Fatalf("unexpected source %+v", s)
	}
	var nilSrc *hookSource
	if got := nilSrc.remap(err); got != err {
		t.Fatal("nil source changed the error")
	}
}
