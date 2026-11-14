package autotls

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/pockethost/pocketone/ext/pluginkit"
	"golang.org/x/crypto/acme/autocert"
)

// serveEvent runs the app's OnServe hooks the way apis.Serve does, with PocketBase's own TLS config and host list.
func serveEvent(t *testing.T, app core.App) (*core.ServeEvent, *tls.Config) {
	t.Helper()
	original := &tls.Config{MinVersion: tls.VersionTLS12}
	e := &core.ServeEvent{
		App:         app,
		Server:      &http.Server{TLSConfig: original},
		CertManager: &autocert.Manager{HostPolicy: autocert.HostWhitelist("example.com", "www.example.com")},
	}
	if err := app.OnServe().Trigger(e, func(*core.ServeEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return e, original
}

func TestAppOffLeavesPocketBaseTLS(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	pluginkit.MustRegister(app, NewApp(Config{}))
	e, original := serveEvent(t, app)
	if e.Server.TLSConfig != original {
		t.Fatal("auto off must not touch the server's TLS config")
	}
}

func TestAppRejectsWildcardWithoutDNS(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	err = NewApp(Config{Auto: true, Domains: []string{"*.example.com"}}).Register(app, nil)
	if err == nil || !strings.Contains(err.Error(), "needs a DNS provider") {
		t.Fatalf("got %v", err)
	}
}

func TestAppSettingsLayerTurnsItOn(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	layers := pluginkit.Config{{"auto": true, "ca": "letsencrypt-staging"}}
	if err := NewApp(Config{}).Register(app, layers); err != nil {
		t.Fatal(err)
	}
	e, original := serveEvent(t, app)
	if e.Server.TLSConfig == original {
		t.Fatal("auto on from a settings layer should swap the TLS config")
	}
}

func TestAppSwapsTLSAndRefusesUnknownHosts(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	pluginkit.MustRegister(app, NewApp(Config{Auto: true, CA: "letsencrypt-staging"}))
	e, original := serveEvent(t, app)
	cfg := e.Server.TLSConfig
	if cfg == original || cfg.GetCertificate == nil {
		t.Fatal("auto on should swap in CertMagic's TLS config")
	}
	if !containsString(cfg.NextProtos, "acme-tls/1") || cfg.NextProtos[0] != "h2" {
		t.Errorf("ALPN: %v", cfg.NextProtos)
	}

	// A host outside PocketBase's list and Config.Domains is refused at the handshake, with no ACME traffic.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	srvErr := make(chan error, 1)
	go func() { srvErr <- tls.Server(serverConn, cfg).Handshake() }()
	client := tls.Client(clientConn, &tls.Config{ServerName: "random.example.org", InsecureSkipVerify: true})
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if err := client.Handshake(); err == nil {
		t.Fatal("handshake for an unknown host should fail")
	}
	if err := <-srvErr; err == nil || !strings.Contains(err.Error(), "random.example.org") {
		t.Fatalf("server side: %v", err)
	}
}
