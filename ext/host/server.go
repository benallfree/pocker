package host

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Listeners are where an edge accepts requests.
type Listeners struct {
	Listen          string    // main address, e.g. ":443" (TLS when TLS or a provider gives a certificate)
	PlainListen     []string  // extra plain-HTTP addresses, e.g. ":8080" on a private network for other edges
	TLS             TLSConfig // certificate files; tls.http_listen redirects plain HTTP to HTTPS
	ShutdownTimeout time.Duration
	HealthPath      string // exempt from the HTTPS redirect ("" = DefaultHealthPath)
}

// Serve listens and serves handler until ctx is done (or a listener fails), then shuts the servers down gracefully.
// Requests' contexts derive from base (nil = context.Background()), which the caller cancels after Serve returns
// to end long-lived responses (realtime). provider, when set, supplies TLS (e.g. automatic certificates); served
// reports whether a hostname belongs to an instance. onListen runs once the main listener is up.
func Serve(ctx context.Context, base context.Context, l Listeners, handler http.Handler, provider TLSProvider,
	served func(host string) bool, log *slog.Logger, onListen func(addr string)) error {
	if log == nil {
		log = slog.Default()
	}
	if base == nil {
		base = context.Background()
	}
	healthPath := l.HealthPath
	if healthPath == "" {
		healthPath = DefaultHealthPath
	}
	ln, err := net.Listen("tcp", l.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Minute,
		// large uploads/downloads, like `pocketbase serve`
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		BaseContext:  func(net.Listener) context.Context { return base },
	}
	serveErr := make(chan error, 1+len(l.PlainListen))
	var redirect *http.Server
	startRedirect := func(h http.Handler) {
		if l.TLS.HTTPListen == "" {
			return
		}
		redirect = &http.Server{Addr: l.TLS.HTTPListen, Handler: h, ReadHeaderTimeout: time.Minute}
		go func() {
			if err := redirect.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("edge: http listener", "error", err)
			}
		}()
	}
	var (
		tlsCfg   *tls.Config
		wrapHTTP func(http.Handler) http.Handler
	)
	if provider != nil {
		tlsCfg, wrapHTTP, err = provider.TLS(base, served)
		if err != nil {
			_ = ln.Close()
			return err
		}
	}
	switch {
	case tlsCfg != nil:
		srv.TLSConfig = tlsCfg
		go func() { serveErr <- srv.ServeTLS(ln, "", "") }()
		h := httpsRedirect(handler, healthPath)
		if wrapHTTP != nil {
			h = wrapHTTP(h)
		}
		startRedirect(h)
	case l.TLS.Cert != "" || l.TLS.Key != "":
		certs, err := newCertReloader(l.TLS.Cert, l.TLS.Key)
		if err != nil {
			_ = ln.Close()
			return err
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.GetCertificate}
		go func() { serveErr <- srv.ServeTLS(ln, "", "") }()
		startRedirect(httpsRedirect(handler, healthPath))
	default:
		go func() { serveErr <- srv.Serve(ln) }()
	}
	if onListen != nil {
		onListen(ln.Addr().String())
	}
	var plain []*http.Server
	for _, addr := range l.PlainListen {
		pln, err := net.Listen("tcp", addr)
		if err != nil {
			_ = srv.Close()
			for _, p := range plain {
				_ = p.Close()
			}
			return fmt.Errorf("plain_listen %s: %w", addr, err)
		}
		p := &http.Server{
			Handler: handler, ReadHeaderTimeout: time.Minute, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute,
			BaseContext: func(net.Listener) context.Context { return base },
		}
		plain = append(plain, p)
		go func() {
			if err := p.Serve(pln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- err
			}
		}()
		log.Info("edge: listening (plain HTTP)", "addr", pln.Addr().String())
	}

	var result error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}
	timeout := l.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), timeout)
	defer done()
	_ = srv.Shutdown(shutdownCtx)
	if redirect != nil {
		_ = redirect.Shutdown(shutdownCtx)
	}
	for _, p := range plain {
		_ = p.Shutdown(shutdownCtx)
	}
	return result
}
