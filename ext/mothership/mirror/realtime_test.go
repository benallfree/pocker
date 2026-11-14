package mirror

import (
	"context"
	"testing"
	"time"
)

// Realtime filters through a back-relation into a superuser-only collection reach a superuser subscriber (the PocketOne fix
// in apis/realtime.go); extension views such as "owners of this edge's instances" depend on it.
func TestRealtimeBackRelationFilter(t *testing.T) {
	app, url := mothershipApp(t)
	e := save(t, app, "edges", "", map[string]any{"edge_id": "e1"})
	o := save(t, app, "owners", "", map[string]any{"name": "x"})
	save(t, app, "instances", "", map[string]any{"name": "i1", "edge": e.Id, "owner": o.Id})
	c := newClient(url, "ms@test.local", "pw-12345678", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, f := range []string{`name != ""`, `instances_via_owner.edge ?= "` + e.Id + `"`, `instances_via_owner.name ?= "i1"`} {
		ev, err := c.subscribe(ctx, []string{topicOf(View{Collection: "owners", Filter: f})})
		if err != nil {
			t.Fatal(err)
		}
		recs, _ := c.list(ctx, "owners", f, "")
		save(t, app, "owners", o.Id, map[string]any{"name": "x", "suspended": true})
		select {
		case <-ev:
			t.Logf("%s: event (list %d)", f, len(recs))
		case <-time.After(2 * time.Second):
			t.Errorf("%s: nothing (list %d)", f, len(recs))
		}
	}
}
