package pocker

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Every pocker.yaml field must survive LoadConfig (it copies fields one by one over the defaults).
func TestLoadConfigReadsEveryField(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pocker.yaml")
	yaml := `
listen: ":9443"
plain_listen: [":9080"]
pb_root: /srv/pb
dev: true
instances_file: /srv/instances.yaml
hooks_pool_size: 4
shutdown_timeout: 30s
max_js_execution: 5s
egress_allow: [10.0.0.0/8]
firewall: { allow_cidrs: [10.0.0.0/8], rate_limit_header_prefix: X-Test } # a plugin's key: kept in Doc()
tls: { cert: /c.pem, key: /k.pem, http_listen: ":9081" }
memory_limit: 1000 # removed keys: ignored, as old configs still carry them
memory_soft_limit: 500
`
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultConfig()
	v, d := reflect.ValueOf(cfg), reflect.ValueOf(def)
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if f.Name == "Limits" || f.PkgPath != "" {
			continue // inline limits: covered by TestLimitsYAML; unexported: the raw doc, checked below
		}
		if reflect.DeepEqual(v.Field(i).Interface(), d.Field(i).Interface()) {
			t.Errorf("%s (%s) was not read from the file", f.Name, f.Tag.Get("yaml"))
		}
	}
	if cfg.PlainListen[0] != ":9080" || cfg.TLS.HTTPListen != ":9081" {
		t.Fatalf("%+v", cfg)
	}
	if n := TopLevel(cfg.Doc(), "firewall"); n == nil || TopLevel(n, "rate_limit_header_prefix").Value != "X-Test" {
		t.Fatal("plugin keys are not available through Doc()")
	}
}
