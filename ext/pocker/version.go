package pocker

import (
	"regexp"
	"runtime/debug"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase"
)

// coreModule is PocketOne's core module path (kept from upstream so upstream merges stay easy).
const coreModule = "ganigeorgiev.com/pocketbase/pocketbase"

// pseudoRe matches a Go pseudo-version: v0.0.2-0.20261114103928-87f81fd2a1b2 (a commit after v0.0.1).
var pseudoRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)-(?:[0-9A-Za-z.]*\.)?0\.\d{14}-([0-9a-f]{12})$`)

// Version is the PocketOne version this binary runs, for X-Powered-By ("PocketOne 0.0.2", "Pocker 0.0.2"): the release
// version a PocketOne release build stamps in (pocketbase.Version), else the PocketOne version a dependent build pins
// (a commit between releases reads "0.0.2-dev+87f81fd2"), else the commit of a local build ("dev+87f81fd2").
func Version() string {
	return versionFrom(pocketbase.Version, readBuildInfo())
}

func readBuildInfo() *debug.BuildInfo {
	bi, _ := debug.ReadBuildInfo()
	return bi
}

func versionFrom(stamped string, bi *debug.BuildInfo) string {
	if stamped != "" && stamped != "(untracked)" {
		return strings.TrimPrefix(stamped, "v")
	}
	if bi == nil {
		return "dev"
	}
	for _, d := range bi.Deps {
		if d.Path != coreModule {
			continue
		}
		v := d.Version
		if d.Replace != nil {
			v = d.Replace.Version // e.g. github.com/pockethost/pocketone v0.0.2-0.2026…; "" for a local path
		}
		if v != "" && v != "(devel)" {
			if m := pseudoRe.FindStringSubmatch(v); m != nil {
				return m[1] + "-dev+" + m[2][:8]
			}
			return strings.TrimPrefix(v, "v")
		}
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 8 {
			return "dev+" + s.Value[:8]
		}
	}
	return "dev"
}
