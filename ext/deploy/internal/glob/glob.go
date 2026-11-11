// Package glob matches slash-separated paths against minimatch-style patterns: path.Match per segment, plus "**"
// for any number of segments. Wildcards match dotfiles too (the npm CLI globbed with dot: true).
package glob

import (
	"path"
	"strings"
)

// Match reports whether name (a/b/c) matches pattern.
func Match(pattern, name string) bool {
	return matchSegs(split(pattern), split(name))
}

// Any reports whether name matches one of patterns.
func Any(patterns []string, name string) bool {
	for _, p := range patterns {
		if Match(p, name) {
			return true
		}
	}
	return false
}

// CouldMatchUnder reports whether some path inside dir could match pattern (to prune a directory walk).
func CouldMatchUnder(pattern, dir string) bool {
	p, d := split(pattern), split(dir)
	for i, seg := range d {
		if i >= len(p) {
			return false
		}
		if p[i] == "**" {
			return true
		}
		if ok, _ := path.Match(p[i], seg); !ok {
			return false
		}
	}
	return len(p) > len(d)
}

// AnyUnder is [CouldMatchUnder] for any of patterns.
func AnyUnder(patterns []string, dir string) bool {
	for _, p := range patterns {
		if CouldMatchUnder(p, dir) {
			return true
		}
	}
	return false
}

func split(s string) []string {
	s = strings.Trim(strings.TrimPrefix(s, "./"), "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegs(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			// collapse repeated **
			for len(p) > 1 && p[1] == "**" {
				p = p[1:]
			}
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(n); i++ {
				if matchSegs(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		if ok, _ := path.Match(p[0], n[0]); !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}
