package autotls

import (
	"context"
	"path/filepath"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"github.com/pockethost/pocketone/ext/ceplugin"
)

// AppStorageDirName is the folder in pb_data where a single app keeps its certificates, unless Config.Storage
// says otherwise. It's separate from PocketBase's own autocert cache (`.autocert_cache`).
const AppStorageDirName = ".autotls"

// App is the single-app plugin ([ceplugin.Plugin]). With Auto on, it swaps the certificate source of PocketBase's
// HTTPS server for CertMagic. It only matters when the app serves HTTPS (`pocketbase serve example.com` or
// `--https`); on plain HTTP it's idle. With Auto off it does nothing, and PocketBase's built-in autocert stays.
//
// Hostnames that may get a certificate: Config.Domains (obtained at start; wildcards need Config.DNS) plus the
// hosts PocketBase itself allows (the domains given to `serve` and their www. variants), issued on first use.
// Challenges: TLS-ALPN-01 on the HTTPS port, or DNS-01 with a DNS provider. HTTP-01 is off, because port 80 is
// PocketBase's redirect server.
//
// It's a separate type from [AutoTLS] on purpose: pocker runs every ceplugin.Plugin on each instance, and
// certificates there belong to the pocker server, not to instances.
type App struct {
	cfg Config
}

// NewApp returns the single-app plugin with cfg as its defaults.
func NewApp(cfg Config) *App { return &App{cfg: cfg} }

// Name is the plugin's name.
func (a *App) Name() string { return "autotls" }

// Register checks the settings and installs the OnServe hook that swaps the server's TLS config.
func (a *App) Register(app core.App, layers ceplugin.Config) error {
	cfg := a.cfg
	if err := layers.Decode(&cfg); err != nil {
		return err
	}
	if !cfg.Auto {
		return nil
	}
	if err := validateAutoTLS(cfg, false); err != nil {
		return err
	}

	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "autotlsServe",
		Func: func(e *core.ServeEvent) error {
			ctx, cancel := context.WithCancel(context.Background())
			e.App.OnTerminate().BindFunc(func(te *core.TerminateEvent) error {
				cancel()
				return te.Next()
			})

			policy := e.CertManager // PocketBase's own host list: serve's domains + www.
			allow := func(host string) bool {
				return policy != nil && policy.HostPolicy != nil && policy.HostPolicy(ctx, normalizeHost(host)) == nil
			}

			auto, err := newAutoTLS(ctx, cfg, false, filepath.Join(e.App.DataDir(), AppStorageDirName), allow, false)
			if err != nil {
				cancel()
				return err
			}
			e.Server.TLSConfig = auto.tlsConfig()
			e.App.Logger().Debug("autotls: CertMagic serves HTTPS certificates", "ca", caURL(cfg.CA), "domains", cfg.Domains)

			return e.Next()
		},
	})
	return nil
}
