package pocker

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

// hooksDebounce groups the burst of events one save or upload makes into a single restart.
const hooksDebounce = 200 * time.Millisecond

// hooksWatcher watches the pb_hooks folder of every running app with one inotify instance (PocketBase's own
// HooksWatch opens one per app, and Linux allows only 128 per user by default) and restarts just the app whose
// hooks changed.
type hooksWatcher struct {
	w       *fsnotify.Watcher
	log     *slog.Logger
	restart func(id string)

	mu     sync.Mutex
	dirs   map[string]string   // watched dir -> instance id
	byID   map[string][]string // instance id -> its watched dirs
	timers map[string]*time.Timer
}

// newHooksWatcher returns nil (and logs) when the platform watcher can't be created; a nil watcher does nothing.
func newHooksWatcher(log *slog.Logger, restart func(id string)) *hooksWatcher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn("pocker: pb_hooks changes won't restart apps", "error", err)
		return nil
	}
	h := &hooksWatcher{w: w, log: log, restart: restart, dirs: map[string]string{}, byID: map[string][]string{}, timers: map[string]*time.Timer{}}
	go h.run()
	return h
}

// add watches id's hooks dir and its subfolders (not hidden ones or node_modules, like PocketBase's watcher).
func (h *hooksWatcher) add(id, hooksDir string) {
	if h == nil {
		return
	}
	_ = filepath.WalkDir(hooksDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != hooksDir && skipHooksDir(d.Name()) {
			return filepath.SkipDir
		}
		h.addDir(id, path)
		return nil
	})
}

func skipHooksDir(name string) bool { return strings.HasPrefix(name, ".") || name == "node_modules" }

func (h *hooksWatcher) addDir(id, dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.dirs[dir]; ok {
		return
	}
	if err := h.w.Add(dir); err != nil {
		h.log.Warn("pocker: can't watch pb_hooks", "id", id, "dir", dir, "error", err)
		return
	}
	h.dirs[dir] = id
	h.byID[id] = append(h.byID[id], dir)
}

// remove stops watching id's hooks (its app stopped).
func (h *hooksWatcher) remove(id string) {
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

func (h *hooksWatcher) close() {
	if h != nil {
		_ = h.w.Close()
	}
}

func (h *hooksWatcher) run() {
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
			h.log.Warn("pocker: pb_hooks watch error", "error", err)
		}
	}
}

func (h *hooksWatcher) handle(ev fsnotify.Event) {
	h.mu.Lock()
	id, ok := h.dirs[filepath.Dir(ev.Name)]
	h.mu.Unlock()
	if !ok {
		return
	}
	if ev.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() && !skipHooksDir(fi.Name()) {
			h.add(id, ev.Name) // a new subfolder: watch it too
		}
	}
	if skipHooksDir(filepath.Base(ev.Name)) {
		return // e.g. an editor's .swp file or node_modules appearing
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.timers[id]; t != nil {
		t.Stop()
	}
	h.timers[id] = time.AfterFunc(hooksDebounce, func() {
		h.mu.Lock()
		delete(h.timers, id)
		h.mu.Unlock()
		h.restart(id)
	})
}
