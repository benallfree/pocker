package edge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/pockethost/pocketone/ext/pluginkit"
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

// EdgeSource tells the router where other edges are. Built in: [YAMLProvider] (a config file's `edges.list`),
// combined with others through [MultiProvider]. Others come from outside this package: ext/mothership's mirror
// follows a mothership's `edges` collection; a host application can bring its own. Instances name their owner edge
// in [Instance.Remote].
type EdgeSource interface {
	// Self is this edge's id ("" when it doesn't know).
	Self() string
	// Resolve maps an instance's Remote value to its edge. ok is false when Remote doesn't name a known edge.
	Resolve(remote string) (e Edge, ok bool)
	// Edges lists every known edge.
	Edges() []Edge
}

// Runner is an [EdgeSource] with background work. The server runs it until shutdown.
type Runner interface {
	Run(ctx context.Context)
}

// EdgesConfig is a config file's `edges:` section: this edge's id and a fixed list of the others.
//
//	edges:
//	  self: lax-1
//	  list:
//	    - { id: sgp-1, internal_url: "http://10.88.0.4:8080", region: sgp }
type EdgesConfig struct {
	Self string       `yaml:"self"`
	List []StaticEdge `yaml:"list"`
}

// EdgesFromConfig builds the [YAMLProvider] for an `edges:` section (node may be nil: no section, nil result).
func EdgesFromConfig(node *yaml.Node, log *slog.Logger) (EdgeSource, error) {
	if node == nil {
		return nil, nil
	}
	var c EdgesConfig
	if err := node.Decode(&c); err != nil {
		return nil, fmt.Errorf("edges: %w", err)
	}
	return c.Build(log)
}

// Build returns the [YAMLProvider] c describes.
func (c EdgesConfig) Build(log *slog.Logger) (EdgeSource, error) {
	for i, e := range c.List {
		if e.ID == "" || e.InternalURL == "" {
			return nil, fmt.Errorf("edges.list[%d] needs id and internal_url", i)
		}
	}
	return &YAMLProvider{SelfID: c.Self, List: c.List}, nil
}

// LoadEdges reads the `edges:` section of a YAML config file (nil, nil when the file has none).
func LoadEdges(path string, log *slog.Logger) (EdgeSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	src, err := EdgesFromConfig(TopLevel(&doc, "edges"), log)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return src, nil
}

// YAMLProvider is a fixed list of edges, e.g. a config file's `edges.list`. An instance's Remote names its owner as
// "edge:@<id>", "edge:<id>" or just "<id>"; health is learned by probing (Status stays "").
type YAMLProvider struct {
	SelfID string       `yaml:"self"`
	List   []StaticEdge `yaml:"list"`
}

// StaticEdge is one entry of [YAMLProvider].
type StaticEdge struct {
	ID          string `yaml:"id"`
	InternalURL string `yaml:"internal_url"`
	Region      string `yaml:"region"`
}

var _ EdgeSource = (*YAMLProvider)(nil)

// Self implements [EdgeSource].
func (s *YAMLProvider) Self() string { return s.SelfID }

// Resolve implements [EdgeSource].
func (s *YAMLProvider) Resolve(remote string) (Edge, bool) {
	id := strings.TrimPrefix(strings.TrimPrefix(remote, "edge:"), "@")
	for _, e := range s.List {
		if e.ID == id {
			return Edge{ID: e.ID, InternalURL: e.InternalURL, Region: e.Region}, true
		}
	}
	return Edge{}, false
}

// Edges implements [EdgeSource].
func (s *YAMLProvider) Edges() []Edge {
	out := make([]Edge, 0, len(s.List))
	for _, e := range s.List {
		out = append(out, Edge{ID: e.ID, InternalURL: e.InternalURL, Region: e.Region})
	}
	return out
}

// MultiProvider asks each source in turn: the first that resolves a Remote wins; Self is the first non-empty one.
// It runs every source that is a [Runner].
type MultiProvider []EdgeSource

var (
	_ EdgeSource = MultiProvider(nil)
	_ Runner     = MultiProvider(nil)
)

// Self implements [EdgeSource].
func (m MultiProvider) Self() string {
	for _, s := range m {
		if id := s.Self(); id != "" {
			return id
		}
	}
	return ""
}

// Resolve implements [EdgeSource].
func (m MultiProvider) Resolve(remote string) (Edge, bool) {
	for _, s := range m {
		if e, ok := s.Resolve(remote); ok {
			return e, true
		}
	}
	return Edge{}, false
}

// Edges implements [EdgeSource].
func (m MultiProvider) Edges() []Edge {
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

// Run implements [Runner]: it runs each source that is a Runner, until ctx is done.
func (m MultiProvider) Run(ctx context.Context) {
	done := make(chan struct{})
	n := 0
	for _, s := range m {
		if r, ok := s.(Runner); ok {
			n++
			go func() { r.Run(ctx); done <- struct{}{} }()
		}
	}
	for ; n > 0; n-- {
		<-done
	}
}

// ParsePBTime parses a PocketBase datetime ("2006-01-02 15:04:05.000Z") or RFC 3339.
func ParsePBTime(v string) (time.Time, error) {
	for _, f := range []string{"2006-01-02 15:04:05.000Z", "2006-01-02 15:04:05Z", time.RFC3339Nano} {
		if t, err := time.Parse(f, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", v)
}

// Section is the key of the `edges` section (pocketbase.yaml, ext/config).
var Section = pluginkit.NewSection("edges", EdgesConfig{}, "The fleet: this edge's id and a fixed list of the other edges (id, internal_url, region).")

// FromSection builds the edge provider for the `edges` section (nil, nil when the config file has none).
func FromSection(log *slog.Logger) (EdgeSource, error) {
	c, found := Section.Get()
	if !found {
		return nil, nil
	}
	return c.Build(log)
}
