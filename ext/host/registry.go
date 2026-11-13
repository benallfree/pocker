package host

import (
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// ConfigurableGate is a [Gate] that reads its settings from the config document (e.g. ext/firewall).
type ConfigurableGate interface {
	Gate
	Configure(doc *yaml.Node, dev bool) error
}

type gateFactory struct {
	name     string
	sections []string
	new      func() ConfigurableGate
}

var (
	gatesMu sync.Mutex
	gates   = map[string]gateFactory{}
)

// RegisterGate makes a gate available to every host in this binary: linking its package registers it, and a host
// turns it on when the config has one of its sections (see [ConfiguredGates]). The host never imports the gate's
// package, so a binary that doesn't link it doesn't carry it.
func RegisterGate(name string, sections []string, newGate func() ConfigurableGate) {
	gatesMu.Lock()
	defer gatesMu.Unlock()
	gates[name] = gateFactory{name: name, sections: sections, new: newGate}
}

// ConfiguredGates returns a new gate for every registered gate whose sections appear in doc, by name.
func ConfiguredGates(doc *yaml.Node) []ConfigurableGate {
	gatesMu.Lock()
	list := make([]gateFactory, 0, len(gates))
	for _, f := range gates {
		list = append(list, f)
	}
	gatesMu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	var out []ConfigurableGate
	for _, f := range list {
		for _, s := range f.sections {
			if TopLevel(doc, s) != nil {
				out = append(out, f.new())
				break
			}
		}
	}
	return out
}
