package host

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// TLSConfig enables HTTPS on Listen (pocker.yaml `tls:`), either from certificate files (re-read when they change, so
// a renewal needs no restart). Automatic certificates (ACME) are the autotls plugin, which also reads `tls:`.
//
//	tls:
//	  cert: /etc/letsencrypt/live/example.com/fullchain.pem
//	  key: /etc/letsencrypt/live/example.com/privkey.pem
//	  http_listen: ":80"   # optional: plain HTTP that redirects to HTTPS
type TLSConfig struct {
	Cert       string `yaml:"cert"`
	Key        string `yaml:"key"`
	HTTPListen string `yaml:"http_listen"`
}

// certReloader serves the current certificate, checking the files at most every 30s.
type certReloader struct {
	cert, key string

	mu        sync.Mutex
	current   *tls.Certificate
	stamp     string
	lastCheck time.Time
}

func newCertReloader(cert, key string) (*certReloader, error) {
	r := &certReloader{cert: cert, key: key}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certReloader) fileStamp() string {
	a, errA := os.Stat(r.cert)
	b, errB := os.Stat(r.key)
	if errA != nil || errB != nil {
		return ""
	}
	return fmt.Sprintf("%d/%d/%d/%d", a.ModTime().UnixNano(), a.Size(), b.ModTime().UnixNano(), b.Size())
}

func (r *certReloader) reload() error {
	c, err := tls.LoadX509KeyPair(r.cert, r.key)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	r.current, r.stamp = &c, r.fileStamp()
	return nil
}

func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.lastCheck) > 30*time.Second {
		r.lastCheck = time.Now()
		if st := r.fileStamp(); st != "" && st != r.stamp {
			_ = r.reload() // keep serving the old certificate if the new files don't load (e.g. mid-write)
		}
	}
	return r.current, nil
}

// httpsRedirect answers plain HTTP: health checks and requests already marked HTTPS by a proxy go to next, the rest
// are redirected to https://.
func httpsRedirect(next http.Handler, healthPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthPath || r.Header.Get("X-Forwarded-Proto") == "https" || r.Host == "" {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}
