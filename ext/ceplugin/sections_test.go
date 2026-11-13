package ceplugin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// jsonSource stands in for ext/config: sections as JSON.
type jsonSource map[string]string

func (s jsonSource) DecodeSection(name string, out any) (bool, error) {
	raw, ok := s[name]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal([]byte(raw), out)
}

type testCfg struct {
	Port  int      `json:"port"`
	Hosts []string `json:"hosts"`
	Mode  string   `json:"mode"`
}

var testSection = NewSection("ce_test_section", testCfg{Port: 80, Mode: "default"}, "a test section")

func TestSections(t *testing.T) {
	defer SetSectionSource(nil)

	SetSectionSource(nil)
	if v, found := testSection.Get(); found || v.Port != 80 {
		t.Fatalf("no source: %+v %v", v, found)
	}
	if _, err := testSection.Require(); err == nil || !strings.Contains(err.Error(), "required but missing") {
		t.Fatalf("require without section: %v", err)
	}

	SetSectionSource(jsonSource{"ce_test_section": `{"port": 8090, "hosts": ["a"]}`})
	v, found := testSection.Get()
	if !found || v.Port != 8090 || v.Mode != "default" || len(v.Hosts) != 1 {
		t.Fatalf("file values over defaults: %+v %v", v, found)
	}
	if _, err := testSection.Require(); err != nil {
		t.Fatal(err)
	}

	// a new source replaces the cached value
	SetSectionSource(jsonSource{"ce_test_section": `{"mode": "x"}`})
	if v, _ := testSection.Get(); v.Port != 80 || v.Mode != "x" {
		t.Fatalf("after a new source: %+v", v)
	}

	// a bad value: CheckSections reports it; Get falls back to the defaults
	bad := jsonSource{"ce_test_section": `{"port": "nope"}`}
	SetSectionSource(bad)
	if err := CheckSections(bad); err == nil || !strings.Contains(err.Error(), "ce_test_section") {
		t.Fatalf("check: %v", err)
	}
	if v, _ := testSection.Get(); v.Port != 80 {
		t.Fatalf("bad value should leave defaults: %+v", v)
	}
	if _, err := testSection.Require(); err == nil {
		t.Fatal("require with a bad value")
	}

	if !IsSection("ce_test_section") || IsSection("nope") {
		t.Fatal("IsSection")
	}
	found = false
	for _, info := range Sections() {
		if info.Name == "ce_test_section" && info.Doc == "a test section" && info.Default.(testCfg).Port == 80 {
			found = true
		}
	}
	if !found {
		t.Fatal("not listed")
	}
}

func TestSectionTwoOwners(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "registered twice") {
			t.Fatalf("got %v", r)
		}
	}()
	NewSection("ce_test_section", 0, "")
	_ = errors.New
}
