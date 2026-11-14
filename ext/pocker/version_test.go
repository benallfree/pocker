package pocker

import (
	"runtime/debug"
	"testing"
)

func TestVersion(t *testing.T) {
	dep := func(v string, replace string) *debug.BuildInfo {
		m := &debug.Module{Path: coreModule, Version: v}
		if replace != "" {
			m.Replace = &debug.Module{Path: "github.com/pockethost/pocketone", Version: replace}
		}
		return &debug.BuildInfo{Deps: []*debug.Module{m}}
	}
	local := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "87f81fd2a1b2c3d4"}}}
	for _, c := range []struct {
		stamped string
		bi      *debug.BuildInfo
		want    string
	}{
		{"0.0.2", nil, "0.0.2"},                            // a release build
		{"v0.0.3", local, "0.0.3"},                         // stamped wins
		{"(untracked)", dep("v0.36.0", "v0.0.2"), "0.0.2"}, // a dependent pinned to a release
		{"(untracked)", dep("v0.36.0", "v0.0.2-0.20261114103928-87f81fd2a1b2"), "0.0.2-dev+87f81fd2"},
		{"(untracked)", local, "dev+87f81fd2"},
		{"(untracked)", nil, "dev"},
	} {
		if got := versionFrom(c.stamped, c.bi); got != c.want {
			t.Errorf("versionFrom(%q) = %q, want %q", c.stamped, got, c.want)
		}
	}
}
