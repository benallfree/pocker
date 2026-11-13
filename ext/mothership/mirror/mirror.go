// Package mirror is the edge side of ext/mothership: it follows a mothership over PocketBase's realtime stream and
// serves what it learns to ext/edge as an [edge.InstanceSource] (which instances exist, and which this edge runs) and
// an [edge.EdgeSource] (where the other edges are).
//
// Each edge only receives what is relevant to it. The stream is a set of filtered views:
//
//   - edges:  every edge record (a fleet is small), so requests can be forwarded and health is current
//   - routes: every instance, but only id, name, hosts, edge and updated: the routing table any edge needs, because a
//     load balancer sends visitors to the nearest edge, which forwards to the owner
//   - local:  full instance records (secrets, limits, plugins, ...) only where edge = this edge
//
// An instance moving away shows up in routes (its edge changes), which drops its local record; one moving here
// arrives in local. Extensions (e.g. a multitenant plugin) add views of their own, adjust instances as they are built
// and name the instances a change in their views affects.
//
// Filters cut what each edge downloads and keeps; they are not a security boundary while edges log in as superusers.
package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pockethost/pocketone/ext/edge"
	"gopkg.in/yaml.v3"
)

// Record is a mothership record as JSON (the fields a view asked for).
type Record map[string]any

// String returns a string field ("" when missing or not a string).
func (r Record) String(k string) string { s, _ := r[k].(string); return s }

// Bool returns a bool field.
func (r Record) Bool(k string) bool { b, _ := r[k].(bool); return b }

// Strings returns a JSON list of strings (or a single string) field.
func (r Record) Strings(k string) []string {
	switch v := r[k].(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Decode converts a JSON field into out through YAML, so types with yaml tags (e.g. [edge.Limits]) decode as they
// would from a config file. A missing or null field leaves out unchanged.
func (r Record) Decode(k string, out any) error {
	v, ok := r[k]
	if !ok || v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}

// View is one filtered subscription to a mothership collection. Filter and Fields are PocketBase's (the realtime
// stream applies them too). "{edge}" in Filter is this edge's record id, "{edge_id}" its name.
type View struct {
	Name       string // unique key, e.g. "users"
	Collection string
	Filter     string
	Fields     string // "" = every field
}

// Built-in view names.
const (
	ViewEdges  = "edges"
	ViewRoutes = "routes"
	ViewLocal  = "local"
)

// Extension extends what the mirror follows and how it builds instances (e.g. owners, account status and plans
// from a multitenant plugin). It runs on the mirror's goroutine; it reads other views through the [Mirror].
type Extension interface {
	// Views are the extra views to follow.
	Views() []View
	// Instance adjusts inst, just built from local (this edge's full instance record).
	Instance(m *Mirror, local Record, inst *edge.Instance)
	// Changed reports the instances to rebuild after rec changed in one of its views.
	Changed(m *Mirror, view string, rec Record, deleted bool) (instanceIDs []string)
}

// Config configures a [Mirror].
type Config struct {
	URL      string // the mothership, e.g. http://10.88.0.10:8090
	Email    string // superuser login (the edge's local config carries it)
	Password string

	Self        string // this edge's edge_id
	InternalURL string // where other edges reach this one; set = register the edge record when it is missing
	Region      string // recorded when registering

	// Apex makes "<name>.<apex>" a hostname of every instance (empty = hosts only).
	Apex string

	Heartbeat time.Duration // how often to mark this edge online (default 15s; negative = never)
	Resync    time.Duration // full reload, catching anything missed (default 10m)

	Extensions []Extension
	Logger     *slog.Logger
	HTTPClient *http.Client // default: its own transport
}

// Mirror follows a mothership (see the package doc). Create it with [New]; [Mirror.Snapshot] connects.
type Mirror struct {
	cfg Config
	c   *client
	log *slog.Logger

	mu        sync.RWMutex
	selfRec   string
	views     []View
	topicView map[string]string
	recs      map[string]map[string]Record // view -> record id -> record

	cur     map[string]edge.Instance // built instances, by id (mirror goroutine only)
	events  <-chan event
	started atomic.Bool
	done    <-chan struct{} // the Watch context's
	out     chan edge.Change
}

var (
	_ edge.InstanceSource = (*Mirror)(nil)
	_ edge.EdgeSource     = (*Mirror)(nil)
)

// New returns a mirror for cfg; nothing connects until Snapshot.
func New(cfg Config) *Mirror {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Mirror{cfg: cfg, c: newClient(cfg.URL, cfg.Email, cfg.Password, cfg.HTTPClient), log: log,
		recs: map[string]map[string]Record{}, cur: map[string]edge.Instance{}, out: make(chan edge.Change, 256)}
}

// Snapshot implements [edge.InstanceSource]: it finds (or registers) this edge's record, opens the stream with every
// view subscribed, loads the views and returns the instances.
func (m *Mirror) Snapshot(ctx context.Context) ([]edge.Instance, error) {
	if m.cfg.Self == "" {
		return nil, errors.New("mothership: this edge's id (self) is required")
	}
	if err := m.resolveSelf(ctx); err != nil {
		return nil, err
	}
	m.buildViews()
	m.heartbeat(ctx, "online") // before the load, so this edge's own record is current in it
	if err := m.connect(ctx); err != nil {
		return nil, err
	}
	m.rebuildAll()
	out := make([]edge.Instance, 0, len(m.cur))
	for _, inst := range m.cur {
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	m.log.Info("mothership: mirror loaded", "edge", m.cfg.Self, "routes", len(m.Records(ViewRoutes)),
		"local", len(m.Records(ViewLocal)), "edges", len(m.Records(ViewEdges)))
	return out, nil
}

// Watch implements [edge.InstanceSource]: changes stream until ctx is done. The stream reconnects on its own.
func (m *Mirror) Watch(ctx context.Context) (<-chan edge.Change, error) {
	if m.started.CompareAndSwap(false, true) {
		m.done = ctx.Done()
		go m.loop(ctx)
	}
	return m.out, nil
}

// SelfRecord is this edge's record id at the mothership ("" before Snapshot).
func (m *Mirror) SelfRecord() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.selfRec
}

// Get returns a record of a view, or nil.
func (m *Mirror) Get(view, id string) Record {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.recs[view][id]
}

// Records returns a copy of a view's records, by id.
func (m *Mirror) Records(view string) map[string]Record {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Record, len(m.recs[view]))
	for k, v := range m.recs[view] {
		out[k] = v
	}
	return out
}

// Self implements [edge.EdgeSource].
func (m *Mirror) Self() string { return m.cfg.Self }

// Resolve implements [edge.EdgeSource]: "edge:<record id>", "edge:@<edge_id>" or "<edge_id>".
func (m *Mirror) Resolve(remote string) (edge.Edge, bool) {
	ref := strings.TrimPrefix(remote, "edge:")
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !strings.HasPrefix(ref, "@") {
		if r, ok := m.recs[ViewEdges][ref]; ok {
			return toEdge(r), true
		}
	}
	id := strings.TrimPrefix(ref, "@")
	for _, r := range m.recs[ViewEdges] {
		if r.String("edge_id") == id {
			return toEdge(r), true
		}
	}
	return edge.Edge{}, false
}

// Edges implements [edge.EdgeSource].
func (m *Mirror) Edges() []edge.Edge {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]edge.Edge, 0, len(m.recs[ViewEdges]))
	for _, r := range m.recs[ViewEdges] {
		out = append(out, toEdge(r))
	}
	return out
}

func toEdge(r Record) edge.Edge {
	e := edge.Edge{ID: r.String("edge_id"), InternalURL: r.String("internal_url"), Region: r.String("region"), Status: r.String("status")}
	if e.ID == "" {
		e.ID = r.String("id")
	}
	if v := r.String("last_seen"); v != "" {
		e.LastSeen, _ = edge.ParsePBTime(v)
	}
	return e
}

// resolveSelf finds this edge's record, registering it when missing and InternalURL is set.
func (m *Mirror) resolveSelf(ctx context.Context) error {
	recs, err := m.c.list(ctx, "edges", fmt.Sprintf("edge_id = %q", m.cfg.Self), "id")
	if err != nil {
		return fmt.Errorf("mothership: %w", err)
	}
	if len(recs) > 0 {
		m.mu.Lock()
		m.selfRec = recs[0].String("id")
		m.mu.Unlock()
		return nil
	}
	if m.cfg.InternalURL == "" {
		return fmt.Errorf("mothership: edge %q is not registered (set internal_url to register it)", m.cfg.Self)
	}
	var created Record
	body := map[string]any{"edge_id": m.cfg.Self, "internal_url": m.cfg.InternalURL, "region": m.cfg.Region, "status": "online"}
	if err := m.c.do(ctx, http.MethodPost, "/api/collections/edges/records", body, &created); err != nil {
		return fmt.Errorf("mothership: register edge %q: %w", m.cfg.Self, err)
	}
	m.mu.Lock()
	m.selfRec = created.String("id")
	m.mu.Unlock()
	m.log.Info("mothership: registered this edge", "edge", m.cfg.Self, "record", created.String("id"))
	return nil
}

func (m *Mirror) buildViews() {
	views := []View{
		{Name: ViewEdges, Collection: "edges"},
		{Name: ViewRoutes, Collection: "instances", Fields: "id,name,hosts,edge,updated"},
		{Name: ViewLocal, Collection: "instances", Filter: "edge = {edge}"},
	}
	for _, x := range m.cfg.Extensions {
		views = append(views, x.Views()...)
	}
	rep := strings.NewReplacer("{edge}", fmt.Sprintf("%q", m.selfRec), "{edge_id}", fmt.Sprintf("%q", m.cfg.Self))
	topics := map[string]string{}
	for i := range views {
		views[i].Filter = rep.Replace(views[i].Filter)
		topics[topicOf(views[i])] = views[i].Name
	}
	m.mu.Lock()
	m.views, m.topicView = views, topics
	m.mu.Unlock()
}

// topicOf is the realtime subscription for a view: "<collection>/*", with its filter and fields as options.
func topicOf(v View) string {
	q := map[string]string{}
	if v.Filter != "" {
		q["filter"] = v.Filter
	}
	if v.Fields != "" {
		q["fields"] = v.Fields
	}
	t := v.Collection + "/*"
	if len(q) > 0 {
		b, _ := json.Marshal(map[string]any{"query": q})
		t += "?options=" + url.QueryEscape(string(b))
	}
	return t
}

// connect opens the stream (subscribed first, so nothing between the load and the stream is lost), then loads every
// view. Events queued meanwhile are applied after, and an event older than the loaded record is ignored.
func (m *Mirror) connect(ctx context.Context) error {
	m.mu.RLock()
	views := m.views
	topics := make([]string, 0, len(m.topicView))
	for t := range m.topicView {
		topics = append(topics, t)
	}
	m.mu.RUnlock()
	sort.Strings(topics)
	m.log.Debug("mothership: subscribe", "topics", topics)
	events, err := m.c.subscribe(ctx, topics)
	if err != nil {
		return fmt.Errorf("mothership: stream: %w", err)
	}
	loaded := map[string]map[string]Record{}
	for _, v := range views {
		recs, err := m.c.list(ctx, v.Collection, v.Filter, v.Fields)
		if err != nil {
			return fmt.Errorf("mothership: load %s: %w", v.Name, err)
		}
		byID := make(map[string]Record, len(recs))
		for _, r := range recs {
			byID[r.String("id")] = r
		}
		loaded[v.Name] = byID
	}
	m.mu.Lock()
	m.recs = loaded
	m.events = events
	m.mu.Unlock()
	return nil
}

func (m *Mirror) loop(ctx context.Context) {
	resync := m.cfg.Resync
	if resync <= 0 {
		resync = 10 * time.Minute
	}
	beat := m.cfg.Heartbeat
	if beat == 0 {
		beat = 15 * time.Second
	}
	rt := time.NewTicker(resync)
	defer rt.Stop()
	var beatC <-chan time.Time
	if beat > 0 {
		bt := time.NewTicker(beat)
		defer bt.Stop()
		beatC = bt.C
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		m.heartbeat(sctx, "offline")
	}()
	for {
		m.mu.RLock()
		events := m.events
		m.mu.RUnlock()
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				m.reconnect(ctx)
				continue
			}
			m.apply(ctx, ev)
		case <-rt.C:
			m.reconnect(ctx)
		case <-beatC:
			m.heartbeat(ctx, "online")
		}
	}
}

// reconnect reopens the stream and reloads every view, retrying with backoff until it works or ctx is done.
func (m *Mirror) reconnect(ctx context.Context) {
	wait := time.Second
	for attempt := 1; ctx.Err() == nil; attempt++ {
		m.log.Info("mothership: reloading", "attempt", attempt)
		if err := m.connect(ctx); err == nil {
			m.rebuildAll()
			m.log.Info("mothership: reloaded", "attempt", attempt, "routes", len(m.Records(ViewRoutes)), "local", len(m.Records(ViewLocal)))
			return
		} else if ctx.Err() == nil {
			m.log.Warn("mothership: reload failed", "attempt", attempt, "retry_in", wait, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < 30*time.Second {
			wait *= 2
		}
	}
}

func (m *Mirror) heartbeat(ctx context.Context, status string) {
	if m.cfg.Heartbeat < 0 || m.SelfRecord() == "" {
		return
	}
	body := map[string]any{"status": status, "last_seen": time.Now().UTC().Format("2006-01-02 15:04:05.000Z")}
	if err := m.c.do(ctx, http.MethodPatch, "/api/collections/edges/records/"+m.SelfRecord(), body, nil); err != nil && ctx.Err() == nil {
		m.log.Warn("mothership: heartbeat", "status", status, "error", err)
	}
}

// apply folds one stream event into its view and rebuilds the instances it affects.
func (m *Mirror) apply(ctx context.Context, ev event) {
	m.mu.RLock()
	view, ok := m.topicView[ev.Name]
	m.mu.RUnlock()
	if !ok {
		return
	}
	var msg struct {
		Action string `json:"action"`
		Record Record `json:"record"`
	}
	if err := json.Unmarshal(ev.Data, &msg); err != nil || msg.Record == nil {
		return
	}
	id := msg.Record.String("id")
	deleted := msg.Action == "delete"
	m.mu.Lock()
	_, wasLocal := m.recs[ViewLocal][id]
	byID := m.recs[view]
	if byID == nil {
		byID = map[string]Record{}
		m.recs[view] = byID
	}
	if deleted {
		delete(byID, id)
	} else if old, ok := byID[id]; ok && old.String("updated") > msg.Record.String("updated") {
		m.mu.Unlock()
		return // older than what we have (queued while loading)
	} else {
		byID[id] = msg.Record
	}
	if view == ViewRoutes && (deleted || msg.Record.String("edge") != m.selfRec) {
		delete(m.recs[ViewLocal], id) // gone, or moved to another edge
	}
	_, isLocal := m.recs[ViewLocal][id]
	m.mu.Unlock()

	if (view == ViewLocal || view == ViewRoutes) && wasLocal != isLocal {
		// this edge gained or lost an instance: records an extension follows because of it (e.g. its owner) don't
		// change, so the stream won't send them; reload the extension views
		m.reloadExtensionViews(ctx)
	}

	var ids []string
	switch view {
	case ViewRoutes, ViewLocal:
		ids = []string{id}
	case ViewEdges:
	default:
		for _, x := range m.cfg.Extensions {
			ids = append(ids, x.Changed(m, view, msg.Record, deleted)...)
		}
	}
	m.rebuild(ids)
}

// reloadExtensionViews lists every extension view again and rebuilds the instances the differences affect.
func (m *Mirror) reloadExtensionViews(ctx context.Context) {
	m.mu.RLock()
	var views []View
	for _, v := range m.views {
		if v.Name != ViewEdges && v.Name != ViewRoutes && v.Name != ViewLocal {
			views = append(views, v)
		}
	}
	m.mu.RUnlock()
	var ids []string
	for _, v := range views {
		recs, err := m.c.list(ctx, v.Collection, v.Filter, v.Fields)
		if err != nil {
			m.log.Warn("mothership: reload view", "view", v.Name, "error", err)
			continue
		}
		next := make(map[string]Record, len(recs))
		for _, r := range recs {
			next[r.String("id")] = r
		}
		m.mu.Lock()
		prev := m.recs[v.Name]
		m.recs[v.Name] = next
		m.mu.Unlock()
		for id, r := range next {
			if !reflect.DeepEqual(prev[id], r) {
				for _, x := range m.cfg.Extensions {
					ids = append(ids, x.Changed(m, v.Name, r, false)...)
				}
			}
		}
		for id, r := range prev {
			if _, ok := next[id]; !ok {
				for _, x := range m.cfg.Extensions {
					ids = append(ids, x.Changed(m, v.Name, r, true)...)
				}
			}
		}
	}
	m.rebuild(ids)
}

// rebuildAll rebuilds every instance and emits the differences.
func (m *Mirror) rebuildAll() {
	ids := map[string]bool{}
	for id := range m.Records(ViewRoutes) {
		ids[id] = true
	}
	for id := range m.cur {
		ids[id] = true
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	m.rebuild(list)
}

func (m *Mirror) rebuild(ids []string) {
	for _, id := range ids {
		next, ok := m.build(id)
		prev, had := m.cur[id]
		if !ok && had && m.pending(id) {
			continue // moving here: keep serving the old entry (forwarding) until the full record arrives
		}
		switch {
		case ok && (!had || !reflect.DeepEqual(prev, next)):
			m.cur[id] = next
			m.emit(edge.Change{Kind: edge.Upsert, Instance: next})
		case !ok && had:
			delete(m.cur, id)
			m.emit(edge.Change{Kind: edge.Delete, Instance: edge.Instance{ID: id}})
		}
	}
}

func (m *Mirror) emit(c edge.Change) {
	if !m.started.Load() {
		return // before Watch: Snapshot returns m.cur instead
	}
	select {
	case m.out <- c:
	case <-m.done:
	}
}

// build returns the instance for a record id: remote (routing only) when another edge owns it, full when this edge
// does and its local record has arrived; ok is false when it isn't served anywhere (no edge) or isn't known.
func (m *Mirror) build(id string) (edge.Instance, bool) {
	route := m.Get(ViewRoutes, id)
	if route == nil {
		return edge.Instance{}, false
	}
	owner := route.String("edge")
	if owner == "" {
		return edge.Instance{}, false // not placed
	}
	if owner != m.SelfRecord() {
		return edge.Instance{ID: id, Hosts: m.hosts(route), Remote: "edge:" + owner}, true
	}
	local := m.Get(ViewLocal, id)
	if local == nil {
		return edge.Instance{}, false // its full record is on the way
	}
	inst := edge.Instance{
		ID:            id,
		Hosts:         m.hosts(local),
		Version:       local.String("version"),
		Dev:           local.Bool("dev"),
		EncryptionKey: local.String("encryption_key"),
	}
	_ = local.Decode("secrets", &inst.Secrets)
	_ = local.Decode("access", &inst.Access)
	_ = local.Decode("limits", &inst.Limits)
	_ = local.Decode("plugins", &inst.Plugins)
	switch {
	case local.String("suspension") != "":
		inst.Blocked = &edge.Blocked{Status: http.StatusForbidden, Message: local.String("suspension")}
	case !local.Bool("power"):
		inst.Blocked = &edge.Blocked{Status: http.StatusConflict, Message: "instance is powered off"}
	}
	for _, x := range m.cfg.Extensions {
		x.Instance(m, local, &inst)
	}
	return inst, true
}

// pending reports whether an instance is placed on this edge but its full record hasn't arrived yet.
func (m *Mirror) pending(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	route := m.recs[ViewRoutes][id]
	_, local := m.recs[ViewLocal][id]
	return route != nil && route.String("edge") == m.selfRec && !local
}

func (m *Mirror) hosts(r Record) []string {
	var out []string
	if name := r.String("name"); name != "" && m.cfg.Apex != "" {
		out = append(out, name+"."+m.cfg.Apex)
	}
	return append(out, r.Strings("hosts")...)
}
