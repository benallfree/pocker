package edge

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Edge is one server in a fleet: another edge requests can be forwarded to.
type Edge struct {
	ID          string    // e.g. "sgp-1"
	InternalURL string    // where other edges reach it, e.g. http://10.88.0.4:8080
	Region      string    // optional
	Status      string    // "online", "stale", "offline" or "" (unknown: a static list)
	LastSeen    time.Time // zero when unknown
}

// EdgeSource tells the router where other edges are. Providers: [StaticEdges] (a YAML `edges:` section) or a control
// plane's mirror (e.g. PocketHost's mothership feed). Instances name their owner edge in [Instance.Remote].
type EdgeSource interface {
	// Self is this edge's id ("" when it doesn't know).
	Self() string
	// Resolve maps an instance's Remote value to its edge. ok is false when Remote doesn't name a known edge.
	Resolve(remote string) (e Edge, ok bool)
	// Edges lists every known edge.
	Edges() []Edge
}

// StaticEdges is a fixed list of edges, e.g. from a YAML `edges:` section. An instance's Remote names its owner as
// "edge:@<id>" or just "<id>"; health is learned by probing (Status stays "").
//
//	edges:
//	  self: lax-1
//	  list:
//	    - { id: sgp-1, internal_url: "http://10.88.0.4:8080", region: sgp }
//	    - { id: ams-1, internal_url: "http://10.88.0.3:8080", region: ams }
type StaticEdges struct {
	SelfID string       `yaml:"self"`
	List   []StaticEdge `yaml:"list"`
}

// StaticEdge is one entry of [StaticEdges].
type StaticEdge struct {
	ID          string `yaml:"id"`
	InternalURL string `yaml:"internal_url"`
	Region      string `yaml:"region"`
}

var _ EdgeSource = (*StaticEdges)(nil)

// Self implements [EdgeSource].
func (s *StaticEdges) Self() string { return s.SelfID }

// Resolve implements [EdgeSource].
func (s *StaticEdges) Resolve(remote string) (Edge, bool) {
	id := strings.TrimPrefix(strings.TrimPrefix(remote, "edge:"), "@")
	for _, e := range s.List {
		if e.ID == id {
			return Edge{ID: e.ID, InternalURL: e.InternalURL, Region: e.Region}, true
		}
	}
	return Edge{}, false
}

// Edges implements [EdgeSource].
func (s *StaticEdges) Edges() []Edge {
	out := make([]Edge, 0, len(s.List))
	for _, e := range s.List {
		out = append(out, Edge{ID: e.ID, InternalURL: e.InternalURL, Region: e.Region})
	}
	return out
}

// LoadStaticEdges reads the `edges:` section of a YAML config file (nil, nil when the file has none).
func LoadStaticEdges(path string) (*StaticEdges, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Edges *StaticEdges `yaml:"edges"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if doc.Edges == nil {
		return nil, nil
	}
	for i, e := range doc.Edges.List {
		if e.ID == "" || e.InternalURL == "" {
			return nil, fmt.Errorf("%s: edges.list[%d] needs id and internal_url", path, i)
		}
	}
	return doc.Edges, nil
}

// MultiEdges asks each source in turn: the first that resolves a Remote wins; Self is the first non-empty one.
type MultiEdges []EdgeSource

var _ EdgeSource = MultiEdges(nil)

// Self implements [EdgeSource].
func (m MultiEdges) Self() string {
	for _, s := range m {
		if id := s.Self(); id != "" {
			return id
		}
	}
	return ""
}

// Resolve implements [EdgeSource].
func (m MultiEdges) Resolve(remote string) (Edge, bool) {
	for _, s := range m {
		if e, ok := s.Resolve(remote); ok {
			return e, true
		}
	}
	return Edge{}, false
}

// Edges implements [EdgeSource].
func (m MultiEdges) Edges() []Edge {
	var out []Edge
	seen := map[string]bool{}
	for _, s := range m {
		for _, e := range s.Edges() {
			if !seen[e.ID] {
				seen[e.ID] = true
				out = append(out, e)
			}
		}
	}
	return out
}
