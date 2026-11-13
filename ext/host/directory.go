package host

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
)

var validID = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// ValidID reports whether id is a safe instance id: lowercase letters, digits, dots and dashes, starting and ending
// with a letter or digit, no "..". Ids are folder names under a runtime's data root.
func ValidID(id string) bool {
	return validID.MatchString(id) && !strings.Contains(id, "..")
}

// NormalizeHost puts a hostname in canonical form: lowercase, no port, no trailing dot.
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

// DevID is the canonical id dev mode gives a hostname the instance source doesn't map: the hostname in normal form.
// It returns "" when that isn't a safe id (see [ValidID]).
func DevID(host string) string {
	id := NormalizeHost(host)
	if !ValidID(id) {
		return ""
	}
	return id
}

// Directory is the edge's view of its instances, fed by an [InstanceSource]: instances by id and by host. Every stage
// of a request (firewall, routing, runtimes) reads it; runtimes subscribe to changes.
type Directory struct {
	log *slog.Logger

	mu        sync.RWMutex
	instances map[string]Instance // by ID
	hosts     map[string]string   // host -> ID
	ready     bool
	subs      []func(c Change, prev *Instance)
}

// NewDirectory returns an empty directory (not ready until [Directory.Load] or [Directory.Run]).
func NewDirectory(log *slog.Logger) *Directory {
	if log == nil {
		log = slog.Default()
	}
	return &Directory{log: log, instances: map[string]Instance{}, hosts: map[string]string{}}
}

// Subscribe registers fn for every change after the initial load. prev is the instance before the change (nil when
// it's new). Subscribers run synchronously, in order; they must not block.
func (d *Directory) Subscribe(fn func(c Change, prev *Instance)) {
	d.mu.Lock()
	d.subs = append(d.subs, fn)
	d.mu.Unlock()
}

// Load replaces the contents with list and marks the directory ready (subscribers aren't called).
func (d *Directory) Load(list []Instance) {
	d.mu.Lock()
	d.instances, d.hosts = map[string]Instance{}, map[string]string{}
	for _, inst := range list {
		d.upsertLocked(inst)
	}
	d.ready = true
	d.mu.Unlock()
	d.log.Info("edge: instances loaded", "count", len(list))
}

// Run loads src's snapshot (unless first is given, then that is used) and applies its changes until ctx is done.
func (d *Directory) Run(ctx context.Context, src InstanceSource, first []Instance) error {
	if first == nil {
		list, err := src.Snapshot(ctx)
		if err != nil {
			return fmt.Errorf("instance snapshot: %w", err)
		}
		first = list
	}
	d.Load(first)
	changes, err := src.Watch(ctx)
	if err != nil {
		return fmt.Errorf("instance watch: %w", err)
	}
	if changes == nil {
		<-ctx.Done()
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case c, ok := <-changes:
			if !ok {
				return nil
			}
			d.Apply(c)
		}
	}
}

// Apply applies one change and tells the subscribers.
func (d *Directory) Apply(c Change) {
	d.mu.Lock()
	var prev *Instance
	if old, ok := d.instances[c.Instance.ID]; ok {
		prev = &old
	}
	switch c.Kind {
	case Upsert:
		d.upsertLocked(c.Instance)
	case Delete:
		d.removeLocked(c.Instance.ID)
	}
	subs := append([]func(Change, *Instance){}, d.subs...)
	d.mu.Unlock()
	for _, fn := range subs {
		fn(c, prev)
	}
}

// Put adds or replaces one instance without going through the source (e.g. dev mode's on-the-fly instances).
func (d *Directory) Put(inst Instance) { d.Apply(Change{Kind: Upsert, Instance: inst}) }

func (d *Directory) upsertLocked(inst Instance) {
	if !ValidID(inst.ID) {
		d.log.Warn("edge: ignoring instance with an unsafe id", "id", inst.ID)
		return
	}
	d.removeLocked(inst.ID)
	d.instances[inst.ID] = inst
	for _, h := range inst.Hosts {
		h = NormalizeHost(h)
		if other, taken := d.hosts[h]; taken && other != inst.ID {
			d.log.Warn("edge: host claimed by two instances; keeping the first", "host", h, "kept", other, "ignored", inst.ID)
			continue
		}
		d.hosts[h] = inst.ID
	}
}

func (d *Directory) removeLocked(id string) {
	old, ok := d.instances[id]
	if !ok {
		return
	}
	for _, h := range old.Hosts {
		h = NormalizeHost(h)
		if d.hosts[h] == id {
			delete(d.hosts, h)
		}
	}
	delete(d.instances, id)
}

// Lookup returns the instance that serves host.
func (d *Directory) Lookup(host string) (Instance, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	id, ok := d.hosts[NormalizeHost(host)]
	if !ok {
		return Instance{}, false
	}
	inst, ok := d.instances[id]
	return inst, ok
}

// Get returns an instance by id.
func (d *Directory) Get(id string) (Instance, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	inst, ok := d.instances[id]
	return inst, ok
}

// Len is the number of instances.
func (d *Directory) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.instances)
}

// Ready reports whether the first snapshot has loaded.
func (d *Directory) Ready() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.ready
}
