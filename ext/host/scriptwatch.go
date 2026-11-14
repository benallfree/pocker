package host

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// scriptDebounce groups the burst of events one save or upload makes into a single restart.
const scriptDebounce = 200 * time.Millisecond

// ScriptWatcher watches the script folders (pb_hooks, pb_migrations) of every running instance with one inotify
// instance (PocketBase's own HooksWatch opens one per app, and Linux allows only 128 per user by default) and calls
// restart for just the instance whose scripts changed. Runtime providers (pocker, docker) use it so a deploy or an
// edit takes effect without restarting anything else. A nil *ScriptWatcher does nothing.
type ScriptWatcher struct {
	w       *fsnotify.Watcher
	log     *slog.Logger
	restart func(id string)

	mu     sync.Mutex
	dirs   map[string]string   // watched dir -> instance id
	byID   map[string][]string // instance id -> its watched dirs
	timers map[string]*time.Timer
}

// NewScriptWatcher returns nil (and logs) when the platform watcher can't be created; a nil watcher does nothing.
func NewScriptWatcher(log *slog.Logger, restart func(id string)) *ScriptWatcher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn("host: script changes won't restart instances", "error", err)
		return nil
	}
	h := &ScriptWatcher{w: w, log: log, restart: restart, dirs: map[string]string{}, byID: map[string][]string{}, timers: map[string]*time.Timer{}}
	go h.run()
	return h
}

// Add watches one of id's script folders and its subfolders (not hidden ones or node_modules, like PocketBase's
// watcher); call it once per folder (pb_hooks, pb_migrations). A folder that doesn't exist is skipped.
func (h *ScriptWatcher) Add(id, hooksDir string) {
	if h == nil {
		return
	}
	_ = filepath.WalkDir(hooksDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != hooksDir && skipScriptDir(d.Name()) {
			return filepath.SkipDir
		}
		h.addDir(id, path)
		return nil
	})
}

func skipScriptDir(name string) bool { return strings.HasPrefix(name, ".") || name == "node_modules" }

func (h *ScriptWatcher) addDir(id, dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.dirs[dir]; ok {
		return
	}
	if err := h.w.Add(dir); err != nil {
		h.log.Warn("host: can't watch scripts", "id", id, "dir", dir, "error", err)
		return
	}
	h.dirs[dir] = id
	h.byID[id] = append(h.byID[id], dir)
}

// Remove stops watching id's folders (its instance stopped).
func (h *ScriptWatcher) Remove(id string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, dir := range h.byID[id] {
		_ = h.w.Remove(dir)
		delete(h.dirs, dir)
	}
	delete(h.byID, id)
	if t := h.timers[id]; t != nil {
		t.Stop()
		delete(h.timers, id)
	}
}

// Close stops the watcher.
func (h *ScriptWatcher) Close() {
	if h != nil {
		_ = h.w.Close()
	}
}

func (h *ScriptWatcher) run() {
	for {
		select {
		case ev, ok := <-h.w.Events:
			if !ok {
				return
			}
			h.handle(ev)
		case err, ok := <-h.w.Errors:
			if !ok {
				return
			}
			h.log.Warn("host: script watch error", "error", err)
		}
	}
}

func (h *ScriptWatcher) handle(ev fsnotify.Event) {
	h.mu.Lock()
	id, ok := h.dirs[filepath.Dir(ev.Name)]
	h.mu.Unlock()
	if !ok {
		return
	}
	if ev.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() && !skipScriptDir(fi.Name()) {
			h.Add(id, ev.Name) // a new subfolder: watch it too
		}
	}
	if skipScriptDir(filepath.Base(ev.Name)) {
		return // e.g. an editor's .swp file or node_modules appearing
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.timers[id]; t != nil {
		t.Stop()
	}
	h.timers[id] = time.AfterFunc(scriptDebounce, func() {
		h.mu.Lock()
		delete(h.timers, id)
		h.mu.Unlock()
		h.restart(id)
	})
}
