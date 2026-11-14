package edge

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEdgesConfigBuild(t *testing.T) {
	var c EdgesConfig
	_ = yaml.Unmarshal([]byte("{ self: a, list: [ { id: b, internal_url: 'http://b' } ] }"), &c)
	src, err := c.Build(quietLog)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := src.Resolve("edge:@b"); !ok || e.InternalURL != "http://b" || src.Self() != "a" {
		t.Fatalf("got %+v %v self %q", e, ok, src.Self())
	}
	if _, err := (EdgesConfig{List: []StaticEdge{{ID: "b"}}}).Build(quietLog); err == nil {
		t.Fatal("entry without internal_url accepted")
	}
}
