package pocker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

// AutoTLS obtains and renews certificates automatically with ACME (Let's Encrypt by default), using CertMagic,
// Caddy's certificate engine. Enabled with `tls: { auto: true }`.
//
//   - Without a DNS provider, every hostname the instance source maps gets its own certificate on its first TLS
//     handshake (HTTP-01 / TLS-ALPN-01 challenges). Works with any DNS host; no API token needed.
//
//   - With `dns:` (e.g. Cloudflare), `domains` may include wildcards (`*.example.com`), proved through DNS-01.
//
//     tls:
//     auto: true
//     email: ops@example.com
//     ca: letsencrypt            # letsencrypt (default), letsencrypt-staging, zerossl, or an ACME directory URL
//     storage: ./pb_root/.pocker-certs
//     domains: ["*.example.com", "example.com"]   # obtained up front; wildcards need dns
//     dns: { provider: cloudflare, api_token_env: CF_API_TOKEN }
//     http_listen: ":80"         # HTTP-01 challenges + redirect to HTTPS
type DNSConfig struct {
	Provider    string `yaml:"provider"`      // "cloudflare"
	APIToken    string `yaml:"api_token"`     // prefer api_token_env so the token stays out of the file
	APITokenEnv string `yaml:"api_token_env"` // name of the environment variable holding the token
}

// SupportedDNSProviders are the dns.provider values Pocker understands.
var SupportedDNSProviders = []string{"cloudflare"}

func (d *DNSConfig) provider() (certmagic.DNSProvider, error) {
	token := d.APIToken
	if token == "" && d.APITokenEnv != "" {
		token = os.Getenv(d.APITokenEnv)
	}
	switch strings.ToLower(d.Provider) {
	case "cloudflare":
		if token == "" {
			return nil, errors.New("tls.dns: cloudflare needs api_token or api_token_env (a token with Zone.DNS edit)")
		}
		return &cloudflare.Provider{APIToken: token}, nil
	case "":
		return nil, errors.New("tls.dns: provider is required")
	default:
		return nil, fmt.Errorf("tls.dns: unknown provider %q (supported: %s)", d.Provider, strings.Join(SupportedDNSProviders, ", "))
	}
}

func caURL(ca string) string {
	switch strings.ToLower(ca) {
	case "", "letsencrypt":
		return certmagic.LetsEncryptProductionCA
	case "letsencrypt-staging", "staging":
		return certmagic.LetsEncryptStagingCA
	case "zerossl":
		return certmagic.ZeroSSLProductionCA
	default:
		return ca
	}
}

// coveredBy reports whether name is one of domains, or matches a one-level wildcard in them.
func coveredBy(name string, domains []string) bool {
	name = normalizeHost(name)
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == name {
			return true
		}
		if strings.HasPrefix(d, "*.") {
			base := d[1:] // ".example.com"
			if strings.HasSuffix(name, base) && !strings.Contains(strings.TrimSuffix(name, base), ".") && len(name) > len(base) {
				return true
			}
		}
	}
	return false
}

type autoTLS struct {
	magic  *certmagic.Config
	issuer *certmagic.ACMEIssuer
}

// validateAutoTLS checks the auto-TLS settings without touching the network.
func validateAutoTLS(cfg TLSConfig) error {
	if cfg.Cert != "" || cfg.Key != "" {
		return errors.New("tls: use either cert/key files or auto, not both")
	}
	if cfg.DNS != nil {
		if _, err := cfg.DNS.provider(); err != nil {
			return err
		}
	}
	for _, d := range cfg.Domains {
		if strings.Contains(d, "*") && cfg.DNS == nil {
			return fmt.Errorf("tls.domains: wildcard %q needs a DNS provider (tls.dns); per-hostname certificates work without one", d)
		}
	}
	return nil
}

// newAutoTLS builds the certificate manager. allowHost decides which hostnames may get an on-demand certificate
// (the instance source's mapped hosts), so random Host names can't trigger issuance.
func newAutoTLS(ctx context.Context, cfg TLSConfig, defaultStorage string, allowHost func(string) bool) (*autoTLS, error) {
	if err := validateAutoTLS(cfg); err != nil {
		return nil, err
	}
	storage := cfg.Storage
	if storage == "" {
		storage = defaultStorage
	}
	storage, err := filepath.Abs(storage)
	if err != nil {
		return nil, err
	}

	var magic *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return magic, nil },
	})
	magic = certmagic.New(cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: storage},
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: func(_ context.Context, name string) error {
				if coveredBy(name, cfg.Domains) || allowHost(name) {
					return nil
				}
				return fmt.Errorf("no instance serves %q", name)
			},
		},
	})

	tmpl := certmagic.ACMEIssuer{CA: caURL(cfg.CA), Email: cfg.Email, Agreed: true}
	if cfg.DNS != nil {
		p, err := cfg.DNS.provider()
		if err != nil {
			return nil, err
		}
		tmpl.DNS01Solver = &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{DNSProvider: p}}
	}
	issuer := certmagic.NewACMEIssuer(magic, tmpl)
	magic.Issuers = []certmagic.Issuer{issuer}

	if len(cfg.Domains) > 0 {
		if err := magic.ManageAsync(ctx, cfg.Domains); err != nil {
			return nil, fmt.Errorf("tls.domains: %w", err)
		}
	}
	return &autoTLS{magic: magic, issuer: issuer}, nil
}

func (a *autoTLS) tlsConfig() *tls.Config {
	c := a.magic.TLSConfig()
	c.MinVersion = tls.VersionTLS12
	c.NextProtos = append([]string{"h2", "http/1.1"}, c.NextProtos...) // keep acme-tls/1 for TLS-ALPN challenges
	return c
}

// httpHandler answers ACME HTTP-01 challenges, passing everything else to next.
func (a *autoTLS) httpHandler(next http.Handler) http.Handler {
	return a.issuer.HTTPChallengeHandler(next)
}
