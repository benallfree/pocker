package host

import (
	"context"
	"testing"
)

func TestYAMLSourceRequired(t *testing.T) {
	s := &YAMLSource{Path: t.TempDir() + "/missing.yaml", Required: true}
	if _, err := s.Snapshot(context.Background()); err == nil {
		t.Fatal("production must fail without an instances file")
	}
	s.Required = false
	if list, err := s.Snapshot(context.Background()); err != nil || len(list) != 0 {
		t.Fatalf("dev: got %v, %v", list, err)
	}
}

func TestDiffInstances(t *testing.T) {
	prev := []Instance{{ID: "a"}, {ID: "b", Dev: true}}
	next := []Instance{{ID: "b", Dev: false}, {ID: "c"}}
	changes := diffInstances(prev, next)
	got := map[string]ChangeKind{}
	for _, c := range changes {
		got[c.Instance.ID] = c.Kind
	}
	if len(got) != 3 || got["a"] != Delete || got["b"] != Upsert || got["c"] != Upsert {
		t.Fatalf("unexpected changes: %+v", changes)
	}
}
