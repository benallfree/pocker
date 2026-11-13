package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"
)

// YAMLSource reads instances from a YAML file and reloads it when it changes.
//
//	instances:
//	  - id: blog
//	    hosts: [blog.localhost]
//	    secrets: { SMTP_HOST: mail.example.com }
//	  - id: shop
//	    hosts: [shop.localhost, shop.example.com]
//	    blocked: { status: 402, message: "Payment required" }
//
// A missing file means no instances (and is picked up once it appears), unless Required is set.
type YAMLSource struct {
	Path string
	// Required makes a missing file an error (production) instead of an empty list.
	Required bool
	// PollInterval is how often the file is checked for changes (default 2s).
	PollInterval time.Duration
}

type yamlFile struct {
	Instances []Instance `yaml:"instances"`
}

func (s *YAMLSource) load() ([]Instance, error) {
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		if s.Required {
			return nil, fmt.Errorf("instances file %s not found (required unless --dev)", s.Path)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f yamlFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	seen := map[string]bool{}
	for _, inst := range f.Instances {
		if !ValidID(inst.ID) {
			return nil, fmt.Errorf("%s: instance id %q must be lowercase letters, digits, dots and dashes", s.Path, inst.ID)
		}
		if seen[inst.ID] {
			return nil, fmt.Errorf("%s: duplicate instance id %q", s.Path, inst.ID)
		}
		seen[inst.ID] = true
	}
	return f.Instances, nil
}

// Snapshot implements [InstanceSource].
func (s *YAMLSource) Snapshot(ctx context.Context) ([]Instance, error) {
	return s.load()
}

// Watch implements [InstanceSource]. It polls the file and emits upserts for new or changed instances and deletes
// for removed ones. A file that fails to parse is reported and ignored until it is fixed.
func (s *YAMLSource) Watch(ctx context.Context) (<-chan Change, error) {
	interval := s.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}

	current, err := s.load()
	if err != nil {
		return nil, err
	}

	stamp := func() string {
		st, err := os.Stat(s.Path)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("%d/%d", st.ModTime().UnixNano(), st.Size())
	}
	lastStamp := stamp()

	ch := make(chan Change)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			st := stamp()
			if st == lastStamp {
				continue
			}
			lastStamp = st
			next, err := s.load()
			if err != nil {
				slog.Error("pocker: instances file not reloaded", "error", err)
				continue
			}
			for _, c := range diffInstances(current, next) {
				select {
				case ch <- c:
				case <-ctx.Done():
					return
				}
			}
			current = next
		}
	}()
	return ch, nil
}

// diffInstances returns the changes that turn prev into next.
func diffInstances(prev, next []Instance) []Change {
	byID := make(map[string]Instance, len(prev))
	for _, inst := range prev {
		byID[inst.ID] = inst
	}
	var changes []Change
	for _, inst := range next {
		old, ok := byID[inst.ID]
		if !ok || !reflect.DeepEqual(old, inst) {
			changes = append(changes, Change{Kind: Upsert, Instance: inst})
		}
		delete(byID, inst.ID)
	}
	for id := range byID {
		changes = append(changes, Change{Kind: Delete, Instance: Instance{ID: id}})
	}
	return changes
}
