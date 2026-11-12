package edge

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEdgesFromConfig(t *testing.T) {
	var doc yaml.Node
	_ = yaml.Unmarshal([]byte("edges: { self: a, list: [ { id: b, internal_url: 'http://b' } ] }"), &doc)
	src, err := EdgesFromConfig(TopLevel(&doc, "edges"), quietLog)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := src.Resolve("edge:@b"); !ok || e.InternalURL != "http://b" || src.Self() != "a" {
		t.Fatalf("got %+v %v self %q", e, ok, src.Self())
	}
	_ = yaml.Unmarshal([]byte("edges: { list: [ { id: b } ] }"), &doc)
	if _, err := EdgesFromConfig(TopLevel(&doc, "edges"), quietLog); err == nil {
		t.Fatal("entry without internal_url accepted")
	}
	if src, err := EdgesFromConfig(nil, quietLog); src != nil || err != nil {
		t.Fatal("no section should be nil, nil")
	}
}
