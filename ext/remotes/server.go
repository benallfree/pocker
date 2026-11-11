package remotes

import (
	"net/http"
	"sync"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// WhoamiPath is the discovery route.
const WhoamiPath = "/_pocker/whoami"

const featuresKey = "remotes.features"

type features struct {
	mu sync.RWMutex
	m  map[string]string
}

func featuresOf(app core.App) *features {
	return app.Store().GetOrSet(featuresKey, func() any { return &features{m: map[string]string{}} }).(*features)
}

// Advertise lists a feature in app's whoami answer, e.g. Advertise(app, "deploy", "/_pocker/deploy/v1"). Plugins
// call it when they register.
func Advertise(app core.App, feature, path string) {
	f := featuresOf(app)
	f.mu.Lock()
	f.m[feature] = path
	f.mu.Unlock()
}

// Config for the server half. Nothing to set yet.
type Config struct{}

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, cfg Config) {
	if err := Register(app, cfg); err != nil {
		panic(err)
	}
}

// Register adds GET /_pocker/whoami to app and, on a *pocketbase.PocketBase, the remote, login, logout and link
// commands.
func Register(app core.App, _ Config) error {
	featuresOf(app)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET(WhoamiPath, func(e *core.RequestEvent) error { return e.JSON(http.StatusOK, whoami(e)) })
		return se.Next()
	})
	if pb, ok := app.(*pocketbase.PocketBase); ok {
		for _, c := range Commands() {
			pb.RootCmd.AddCommand(c)
		}
	}
	return nil
}

func whoami(e *core.RequestEvent) map[string]any {
	f := featuresOf(e.App)
	f.mu.RLock()
	feats := make(map[string]string, len(f.m)+1)
	for k, v := range f.m {
		feats[k] = v
	}
	f.mu.RUnlock()
	// the tokens plugin doesn't import this one: detect it by its store key
	if _, ok := feats["tokens"]; !ok && e.App.Store().Has("tokens") {
		feats["tokens"] = "/api/tokens"
	}
	out := map[string]any{
		"auth":     nil,
		"via":      "",
		"scopes":   []string{},
		"features": feats,
		"app":      map[string]string{"name": e.App.Settings().Meta.AppName, "url": e.App.Settings().Meta.AppURL},
	}
	if e.Auth != nil {
		out["auth"] = map[string]string{"collection": e.Auth.Collection().Name, "id": e.Auth.Id, "email": e.Auth.Email()}
		out["via"] = "jwt"
		if id, _ := e.Get("tokens.id").(string); id != "" {
			out["via"] = "token"
			out["tokenId"] = id
			if s, _ := e.Get("tokens.scopes").([]string); s != nil {
				out["scopes"] = s
			}
		}
	}
	return out
}
