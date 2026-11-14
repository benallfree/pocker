package lifecycle

import (
	"context"
	"time"

	"github.com/pockethost/pocketone/ext/pluginkit"
)

// Action is what a decision asks this host to do with an instance's data.
type Action string

const (
	// Measure refreshes the instance's disk use.
	Measure Action = "measure"
)

// Decision is one thing to do now, and why.
type Decision struct {
	Instance string
	Action   Action
	Reason   string
}

// PolicyProvider decides what this host does with its instances' data. Built in: [YAMLPolicy] (rules from the
// `lifecycle` section, decided on this host); a control plane provides its own (e.g. one that delegates the
// decisions to a mothership), and providers can wrap each other.
type PolicyProvider interface {
	// Decide returns what to do now, given the facts of every instance this host serves.
	Decide(ctx context.Context, facts []Facts) ([]Decision, error)
	// ReportEvery is how often changed last-request times are reported.
	ReportEvery() time.Duration
	// Concurrency is how many instances are worked on at once.
	Concurrency() int
}

// SectionConfig is the `lifecycle` section.
//
//	lifecycle:
//	  measure_every: 24h   # how old a disk measurement may get
//	  report_every: 1h     # how often changed last-request times are reported
//	  concurrency: 2       # instances measured at once
type SectionConfig struct {
	MeasureEvery time.Duration `yaml:"measure_every"`
	ReportEvery  time.Duration `yaml:"report_every"`
	Concurrency  int           `yaml:"concurrency"`
}

// Section is the key of the `lifecycle` section.
var Section = pluginkit.NewSection("lifecycle", SectionConfig{MeasureEvery: 24 * time.Hour, ReportEvery: time.Hour, Concurrency: 2},
	"Instance data on this host: how often disk use is measured and last-request times are reported.")

// YAMLPolicy decides on this host from the `lifecycle` section (or Config, when set): measure each instance when its
// disk measurement is older than measure_every (or missing).
type YAMLPolicy struct {
	Config *SectionConfig // nil = the `lifecycle` section
	Now    func() time.Time
}

func (p YAMLPolicy) config() SectionConfig {
	c := SectionConfig{MeasureEvery: 24 * time.Hour, ReportEvery: time.Hour, Concurrency: 2}
	src := p.Config
	if src == nil {
		s, _ := Section.Get()
		src = &s
	}
	if src.MeasureEvery > 0 {
		c.MeasureEvery = src.MeasureEvery
	}
	if src.ReportEvery > 0 {
		c.ReportEvery = src.ReportEvery
	}
	if src.Concurrency > 0 {
		c.Concurrency = src.Concurrency
	}
	return c
}

// Decide implements [PolicyProvider].
func (p YAMLPolicy) Decide(_ context.Context, facts []Facts) ([]Decision, error) {
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	every := p.config().MeasureEvery
	var out []Decision
	for _, f := range facts {
		switch {
		case f.MeasuredAt.IsZero():
			out = append(out, Decision{Instance: f.Instance, Action: Measure, Reason: "never measured"})
		case now.Sub(f.MeasuredAt) >= every:
			out = append(out, Decision{Instance: f.Instance, Action: Measure, Reason: "measurement older than " + every.String()})
		}
	}
	return out, nil
}

// ReportEvery implements [PolicyProvider].
func (p YAMLPolicy) ReportEvery() time.Duration { return p.config().ReportEvery }

// Concurrency implements [PolicyProvider].
func (p YAMLPolicy) Concurrency() int { return p.config().Concurrency }
