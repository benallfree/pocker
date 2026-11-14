package docker

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/pockethost/pocketone/ext/host"
)

// InstanceDir is an instance's folder on the host (<DataRoot>/<id>).
func (rt *Runtime) InstanceDir(id string) string { return filepath.Join(rt.cfg.DataRoot, id) }

// WithInstanceStopped stops the instance's container (if running), holds its new requests, runs fn with the
// instance folder, then releases them; the next request starts a fresh container. Use it to change an instance's
// files (deploys, restores) or to work on its databases while nothing has them open.
func (rt *Runtime) WithInstanceStopped(ctx context.Context, id string, fn func(dir string) error) error {
	rt.mu.Lock()
	if rt.held == nil {
		rt.held = map[string]chan struct{}{}
	}
	if _, busy := rt.held[id]; busy {
		rt.mu.Unlock()
		return fmt.Errorf("instance %q is already being updated", id)
	}
	release := make(chan struct{})
	rt.held[id] = release
	rt.mu.Unlock()
	defer func() {
		rt.mu.Lock()
		delete(rt.held, id)
		rt.mu.Unlock()
		close(release)
	}()
	rt.Stop(ctx, id)
	return fn(rt.InstanceDir(id))
}

// StartInstance starts an instance's container now (e.g. after maintenance), instead of on its next request.
func (rt *Runtime) StartInstance(ctx context.Context, inst host.Instance) error {
	_, err := rt.ensure(ctx, rt.get(inst.ID), inst)
	return err
}

// Precheck implements [host.Prechecker]: a request for an instance under maintenance waits for it (up to 30 s), then
// answers 503 with Retry-After.
func (rt *Runtime) Precheck(w http.ResponseWriter, r *http.Request, inst host.Instance) bool {
	rt.mu.Lock()
	release, held := rt.held[inst.ID]
	rt.mu.Unlock()
	if !held {
		return true
	}
	select {
	case <-release:
		return true
	case <-time.After(30 * time.Second):
	case <-r.Context().Done():
		return false
	}
	w.Header().Set("Retry-After", "5")
	http.Error(w, "this instance is being updated; retry shortly", http.StatusServiceUnavailable)
	return false
}

var _ host.Prechecker = (*Runtime)(nil)
