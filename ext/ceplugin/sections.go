package ceplugin

// Typed config sections. A plugin owns one top-level section of the config file (pocketbase.yaml, read by
// ext/config) and exports its key; reading a section requires that key, so a plugin can only read the sections it
// was built against (it imports their owners). There is no lookup by name and no access to the raw file. This
// package only defines the keys; a config loader (ext/config) decodes them.

import (
	"fmt"
	"sort"
	"sync"
)

// SectionSource decodes a section of the loaded config into out (a pointer to the section's type, holding its
// defaults). found is false when the file has no such section. ext/config is the stock implementation.
type SectionSource interface {
	DecodeSection(name string, out any) (found bool, err error)
}

// SectionInfo describes a registered section (for `pocketbase config` and validation).
type SectionInfo struct {
	Name    string
	Doc     string
	Default any // a fresh copy of the defaults
	check   func(SectionSource) error
}

var (
	secMu      sync.RWMutex
	secInfos   = map[string]SectionInfo{}
	secSource  SectionSource
	secVersion int // bumped by SetSectionSource, so cached values are decoded again
)

// Section is the typed key of one config section. Create it with [NewSection] in the owning package and export it;
// other packages read the section through it.
type Section[T any] struct {
	name, doc string
	def       func() T

	mu      sync.Mutex
	version int
	cached  bool
	val     T
	found   bool
	err     error
}

// NewSection registers the section name, owned by the calling package, with def's value as its defaults. It panics if
// another package already registered the name (two owners of one section).
func NewSection[T any](name string, def T, doc string) *Section[T] {
	return NewSectionFunc(name, func() T { return def }, doc)
}

// NewSectionFunc is [NewSection] with defaults built by a function, for types holding maps or slices that must not
// be shared between decodes.
func NewSectionFunc[T any](name string, def func() T, doc string) *Section[T] {
	s := &Section[T]{name: name, doc: doc, def: def}
	secMu.Lock()
	defer secMu.Unlock()
	if _, dup := secInfos[name]; dup {
		panic(fmt.Sprintf("ceplugin: config section %q is registered twice", name))
	}
	secInfos[name] = SectionInfo{Name: name, Doc: doc, Default: def(), check: func(src SectionSource) error {
		v := def()
		_, err := src.DecodeSection(name, &v)
		return err
	}}
	return s
}

// Name is the section's top-level key.
func (s *Section[T]) Name() string { return s.name }

func (s *Section[T]) load() (T, bool, error) {
	secMu.RLock()
	src, ver := secSource, secVersion
	secMu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached && s.version == ver {
		return s.val, s.found, s.err
	}
	v := s.def()
	found := false
	var err error
	if src != nil {
		found, err = src.DecodeSection(s.name, &v)
		if err != nil {
			err = fmt.Errorf("config section %s: %w", s.name, err)
			v = s.def()
		}
	}
	s.val, s.found, s.err, s.cached, s.version = v, found, err, true, ver
	return v, found, err
}

// Get returns the section (its defaults with the file's values applied) and whether the file has it. A section that
// fails to decode was already reported by [CheckSections] at startup; Get then returns the defaults.
func (s *Section[T]) Get() (T, bool) {
	v, found, _ := s.load()
	return v, found
}

// Require returns the section, or an error when the file doesn't have it or it doesn't decode: for plugins that
// can't run without their settings.
func (s *Section[T]) Require() (T, error) {
	v, found, err := s.load()
	if err != nil {
		return v, err
	}
	if !found {
		return v, fmt.Errorf("config section %q is required but missing", s.name)
	}
	return v, nil
}

// SetSectionSource installs the loaded config (ext/config calls it once at startup). Cached sections decode again.
func SetSectionSource(src SectionSource) {
	secMu.Lock()
	secSource = src
	secVersion++
	secMu.Unlock()
}

// Sections lists the registered sections, by name.
func Sections() []SectionInfo {
	secMu.RLock()
	defer secMu.RUnlock()
	out := make([]SectionInfo, 0, len(secInfos))
	for _, info := range secInfos {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// IsSection reports whether name is a registered section.
func IsSection(name string) bool {
	secMu.RLock()
	defer secMu.RUnlock()
	_, ok := secInfos[name]
	return ok
}

// CheckSections decodes every registered section from src and returns the first error (bad values fail at startup,
// not when a plugin first reads its section).
func CheckSections(src SectionSource) error {
	for _, info := range Sections() {
		if err := info.check(src); err != nil {
			return fmt.Errorf("config section %s: %w", info.Name, err)
		}
	}
	return nil
}
