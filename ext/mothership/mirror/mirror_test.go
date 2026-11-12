package mirror

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/pockethost/pocketbase/ext/edge"
	"github.com/pockethost/pocketbase/ext/mothership"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// mothershipApp is a real CE mothership on a local test server: the canonical collections, a superuser, and an
// `owners` collection plus instances.owner standing in for a multitenant extension's schema.
func mothershipApp(t *testing.T) (*tests.TestApp, string) {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	if err := mothership.EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	sc, _ := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	su := core.NewRecord(sc)
	su.SetEmail("ms@test.local")
	su.SetPassword("pw-12345678")
	if err := app.Save(su); err != nil {
		t.Fatal(err)
	}
	owners := core.NewBaseCollection("owners")
	owners.Fields.Add(&core.TextField{Name: "name"}, &core.BoolField{Name: "suspended"},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
	if err := app.Save(owners); err != nil {
		t.Fatal(err)
	}
	ic, _ := app.FindCollectionByNameOrId("instances")
	ic.Fields.Add(&core.RelationField{Name: "owner", CollectionId: owners.Id, MaxSelect: 1})
	if err := app.Save(ic); err != nil {
		t.Fatal(err)
	}

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	err = app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		srv = httptest.NewServer(mux)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	return app, srv.URL
}

func save(t *testing.T, app core.App, coll string, id string, fields map[string]any) *core.Record {
	t.Helper()
	var r *core.Record
	if id != "" {
		r, _ = app.FindRecordById(coll, id)
	}
	if r == nil {
		c, err := app.FindCollectionByNameOrId(coll)
		if err != nil {
			t.Fatal(err)
		}
		r = core.NewRecord(c)
	}
	for k, v := range fields {
		r.Set(k, v)
	}
	if err := app.Save(r); err != nil {
		t.Fatalf("save %s: %v", coll, err)
	}
	return r
}

// ownerExt stands in for a multitenant extension: it follows the owners of this edge's instances only, and blocks
// the instances of a suspended owner.
type ownerExt struct{}

func (ownerExt) Views() []View {
	return []View{{Name: "owners", Collection: "owners", Filter: "instances_via_owner.edge ?= {edge}"}}
}

func (ownerExt) Instance(m *Mirror, local Record, inst *edge.Instance) {
	if o := m.Get("owners", local.String("owner")); o != nil && o.Bool("suspended") {
		inst.Blocked = &edge.Blocked{Status: 403, Message: "account suspended"}
	}
}

func (ownerExt) Changed(m *Mirror, view string, rec Record, deleted bool) []string {
	var ids []string
	for id, l := range m.Records(ViewLocal) {
		if l.String("owner") == rec.String("id") {
			ids = append(ids, id)
		}
	}
	return ids
}

func TestMirror(t *testing.T) {
	app, url := mothershipApp(t)
	sgp := save(t, app, "edges", "", map[string]any{"edge_id": "sgp-1", "internal_url": "http://10.88.0.4:8080", "status": "online"})
	alice := save(t, app, "owners", "", map[string]any{"name": "alice"})
	bob := save(t, app, "owners", "", map[string]any{"name": "bob"})

	m := New(Config{URL: url, Email: "ms@test.local", Password: "pw-12345678", Self: "lax-1",
		InternalURL: "http://10.88.0.5:8080", Region: "lax", Apex: "example.test", Heartbeat: time.Hour,
		Extensions: []Extension{ownerExt{}}, Logger: quiet})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// the edge registers itself on first contact
	mctx, mcancel := context.WithCancel(ctx)
	if _, err := m.Snapshot(mctx); err != nil {
		t.Fatal(err)
	}
	mcancel() // its stream is never watched
	self := m.SelfRecord()
	if self == "" {
		t.Fatal("no self record")
	}
	if e, ok := m.Resolve("lax-1"); !ok || e.InternalURL != "http://10.88.0.5:8080" || e.Status != "online" || e.LastSeen.IsZero() {
		t.Fatalf("self edge: %+v %v", e, ok)
	}

	a := save(t, app, "instances", "", map[string]any{"name": "a", "hosts": []string{"a.custom.test"}, "edge": self, "power": true,
		"secrets": map[string]string{"API_KEY": "k"}, "limits": map[string]any{"max_sleep": "5s"}, "owner": alice.Id})
	b := save(t, app, "instances", "", map[string]any{"name": "b", "edge": sgp.Id, "power": true, "secrets": map[string]string{"B": "secret"}, "owner": bob.Id})
	save(t, app, "instances", "", map[string]any{"name": "c", "power": true}) // not placed
	d := save(t, app, "instances", "", map[string]any{"name": "d", "edge": self})

	m2 := New(Config{URL: url, Email: "ms@test.local", Password: "pw-12345678", Self: "lax-1", Apex: "example.test",
		Heartbeat: time.Hour, Extensions: []Extension{ownerExt{}}, Logger: quiet})
	first, err := m2.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]edge.Instance{}
	for _, i := range first {
		byID[i.ID] = i
	}
	if len(byID) != 3 {
		t.Fatalf("want a, b, d; got %v", first)
	}
	ia := byID[a.Id]
	if ia.Remote != "" || ia.Secrets["API_KEY"] != "k" || ia.Blocked != nil || len(ia.Hosts) != 2 || ia.Hosts[0] != "a.example.test" || ia.Limits.MaxSleep == 0 {
		t.Fatalf("a: %+v", ia)
	}
	if ib := byID[b.Id]; ib.Remote != "edge:"+sgp.Id || ib.Secrets != nil || ib.Hosts[0] != "b.example.test" {
		t.Fatalf("b: %+v", ib)
	}
	if id := byID[d.Id]; id.Blocked == nil || id.Blocked.Status != 409 {
		t.Fatalf("d (powered off): %+v", id)
	}

	// the filters: full records only for this edge; routes carry routing fields only; owners only of local instances
	if _, ok := m2.Records(ViewLocal)[b.Id]; ok {
		t.Fatal("another edge's instance in the local view")
	}
	if _, ok := m2.Records(ViewRoutes)[b.Id]["secrets"]; ok {
		t.Fatal("routes view carries secrets")
	}
	if _, ok := m2.Records("owners")[bob.Id]; ok {
		t.Fatal("owner of another edge's instance in the owners view")
	}

	changes, _ := m2.Watch(ctx)
	next := func(what string) edge.Change {
		t.Helper()
		select {
		case c := <-changes:
			return c
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no change within 5s", what)
		}
		return edge.Change{}
	}

	// another edge's instance changes something only it sees: nothing arrives here
	save(t, app, "instances", b.Id, map[string]any{"secrets": map[string]string{"B": "rotated"}})

	// moved here: the full record arrives
	save(t, app, "instances", b.Id, map[string]any{"edge": self})
	c := next("b moved here")
	for c.Instance.ID == b.Id && c.Instance.Remote != "" { // the routes event may land before the local one
		c = next("b full record")
	}
	if c.Kind != edge.Upsert || c.Instance.ID != b.Id || c.Instance.Secrets["B"] != "rotated" {
		t.Fatalf("b moved here: %+v", c)
	}

	// moved away: remote, and its full record is dropped
	save(t, app, "instances", a.Id, map[string]any{"edge": sgp.Id})
	if c := next("a moved away"); c.Instance.ID != a.Id || c.Instance.Remote != "edge:"+sgp.Id || c.Instance.Secrets != nil {
		t.Fatalf("a moved away: %+v", c)
	}
	if _, ok := m2.Records(ViewLocal)[a.Id]; ok {
		t.Fatal("a's full record kept after it moved")
	}

	// an extension's view: bob (owner of b, now local) is suspended
	waitFor(t, func() bool { _, ok := m2.Records("owners")[bob.Id]; return ok }) // followed once b is local
	save(t, app, "owners", bob.Id, map[string]any{"suspended": true})
	c = next("bob suspended")
	for c.Instance.ID != b.Id {
		c = next("bob suspended")
	}
	if c.Instance.Blocked == nil || c.Instance.Blocked.Message != "account suspended" {
		t.Fatalf("b after suspension: %+v", c)
	}

	// deleted
	if err := app.Delete(d); err != nil {
		t.Fatal(err)
	}
	if c := next("d deleted"); c.Kind != edge.Delete || c.Instance.ID != d.Id {
		t.Fatalf("d deleted: %+v", c)
	}

	// edges stay current: a new edge, resolvable by name and record id
	nyc := save(t, app, "edges", "", map[string]any{"edge_id": "nyc-1", "internal_url": "http://10.88.0.9:8080"})
	waitFor(t, func() bool { _, ok := m2.Resolve("edge:" + nyc.Id); return ok })
	if e, ok := m2.Resolve("edge:@nyc-1"); !ok || e.ID != "nyc-1" {
		t.Fatalf("nyc-1: %+v %v", e, ok)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
	}
}

func TestUnregisteredEdge(t *testing.T) {
	_, url := mothershipApp(t)
	m := New(Config{URL: url, Email: "ms@test.local", Password: "pw-12345678", Self: "ghost", Logger: quiet})
	if _, err := m.Snapshot(context.Background()); err == nil {
		t.Fatal("an unregistered edge without internal_url should fail")
	}
	bad := New(Config{URL: url, Email: "ms@test.local", Password: "nope", Self: "x", Logger: quiet})
	if _, err := bad.Snapshot(context.Background()); err == nil {
		t.Fatal("bad login accepted")
	}
}
