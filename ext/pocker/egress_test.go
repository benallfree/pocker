package pocker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/pockethost/pocketbase/ext/sandbox"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
)

// fakeSMTP accepts one message and returns it on the channel.
func fakeSMTP(t *testing.T, addr string) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("can't listen on %s: %v", addr, err)
	}
	got := make(chan string, 1)
	go func() {
		defer ln.Close()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { fmt.Fprintf(c, "%s\r\n", s) }
		w("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					w("250 queued")
					got <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				w("250 fake")
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				w("354 go ahead")
			case strings.HasPrefix(cmd, "QUIT"):
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestEgressFromApps(t *testing.T) {
	// 127.0.0.2 stands in for an allowed service; everything else private is denied.
	sandbox.InstallEgressPolicy(sandbox.EgressPolicy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.2/32")}})

	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal secret"))
	}))
	defer denied.Close()

	lnAllowed, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("can't listen on 127.0.0.2: %v", err)
	}
	allowed := &httptest.Server{Listener: lnAllowed, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
			return
		}
		_, _ = w.Write([]byte("public hello"))
	})}}
	allowed.Start()
	defer allowed.Close()

	_, deniedPort, _ := net.SplitHostPort(strings.TrimPrefix(denied.URL, "http://"))
	routes := map[string]string{
		"loopback":    denied.URL,
		"localhost":   "http://localhost:" + deniedPort,
		"mapped":      "http://[::ffff:127.0.0.1]:" + deniedPort,
		"mothership":  "http://10.88.0.10:8090/api/health",
		"metadata":    "http://169.254.169.254/latest/meta-data/",
		"allowed":     allowed.URL + "/",
		"allowedBig":  allowed.URL + "/big",
		"fileFromURL": denied.URL,
	}

	root := t.TempDir()
	dir := filepath.Join(root, "a")
	if err := os.MkdirAll(filepath.Join(dir, "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	var hooks strings.Builder
	for name, url := range routes {
		call := `$http.send({ url: "` + url + `", timeout: 3 }).raw`
		if name == "fileFromURL" {
			call = `$filesystem.fileFromURL("` + url + `", 3).size`
		}
		hooks.WriteString(`routerAdd("GET", "/t/` + name + `", (e) => {
  try { return e.json(200, { ok: true, v: ` + call + ` }) }
  catch (err) { return e.json(200, { ok: false, err: String(err) }) }
})
`)
	}
	if err := os.WriteFile(filepath.Join(dir, "pb_hooks/main.pb.js"), []byte(hooks.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	running, err := startApp(appOptions{PBRoot: root, Defaults: Limits{HTTPSendMaxTimeout: 5 * time.Second, HTTPSendMaxBody: 1024}}, Instance{ID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(running)

	call := func(name string) attackResult {
		r := httptest.NewRequest(http.MethodGet, "/t/"+name, nil)
		w := httptest.NewRecorder()
		running.handler.ServeHTTP(w, r)
		var res attackResult
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return res
	}

	for _, name := range []string{"loopback", "localhost", "mapped", "mothership", "metadata", "fileFromURL"} {
		if res := call(name); res.OK || !strings.Contains(res.Err, "not allowed") {
			t.Errorf("%s: expected egress refusal, got %+v", name, res)
		}
	}
	if res := call("allowed"); !res.OK || res.V != "public hello" {
		t.Errorf("allowed: expected success, got %+v", res)
	}
	if res := call("allowedBig"); res.OK || !strings.Contains(res.Err, "larger than") {
		t.Errorf("allowedBig: expected the body cap, got %+v", res)
	}

	// The app's own SMTP goes through the same dialer (sandbox.BindMail): denied on loopback, delivered on the
	// allowed address
	send := func(addr string) error {
		host, port, _ := net.SplitHostPort(addr)
		var p int
		fmt.Sscanf(port, "%d", &p)
		smtpCfg := &running.app.Settings().SMTP
		smtpCfg.Enabled, smtpCfg.Host, smtpCfg.Port = true, host, p
		return running.app.NewMailClient().Send(&mailer.Message{
			From:    mail.Address{Address: "app@example.com"},
			To:      []mail.Address{{Address: "user@example.com"}},
			Bcc:     []mail.Address{{Address: "hidden@example.com"}},
			Subject: "pocker egress test",
			Text:    "hello",
		})
	}
	deniedSMTP, _ := fakeSMTP(t, "127.0.0.1:0")
	if err := send(deniedSMTP); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("smtp to loopback: expected refusal, got %v", err)
	}
	allowedSMTP, got := fakeSMTP(t, "127.0.0.2:0")
	if err := send(allowedSMTP); err != nil {
		t.Fatalf("smtp to allowed host: %v", err)
	}
	select {
	case msg := <-got:
		if !strings.Contains(msg, "pocker egress test") || strings.Contains(strings.ToLower(msg), "hidden@example.com") {
			t.Errorf("unexpected message (subject missing or Bcc leaked into headers):\n%s", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake SMTP server got no message")
	}
}
