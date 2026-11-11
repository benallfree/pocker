package pockerplugin

import (
	"testing"
	"time"

	"github.com/pockethost/pocketbase/ext/pocker"
)

func TestInstanceConfig(t *testing.T) {
	ac, err := instanceConfig(pocker.Instance{ID: "a", Plugins: map[string]any{Name: map[string]any{"enabled": false, "ttl": "2m"}}})
	if err != nil {
		t.Fatal(err)
	}
	if ac.Enabled == nil || *ac.Enabled || ac.TTL != 2*time.Minute {
		t.Fatalf("got %+v", ac)
	}
	if _, err := instanceConfig(pocker.Instance{ID: "a", Plugins: map[string]any{Name: map[string]any{"target": "x"}}}); err == nil {
		t.Fatal("per-instance target must be refused")
	}
}
