// Package tokens adds API tokens to a PocketBase app: long-lived bearer secrets (pbt_...) owned by an auth record,
// optionally limited to scopes that plugins register, listed and revoked one by one. A request with
// `Authorization: Bearer pbt_...` is authenticated as the token's owner; JWTs keep working unchanged.
//
//	app := pocketbase.New()
//	tokens.MustRegister(app, tokens.Config{})
//
// It adds the _tokens collection, the /api/tokens routes, an "API tokens" screen under Settings and, on a
// *pocketbase.PocketBase, the `token` command.
package tokens

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// Prefix starts every token secret, so secret scanners can find leaked ones.
const Prefix = "pbt_"

const (
	storeKey      = "tokens"
	secretLen     = 40 // random alphanumerics after the prefix (~238 bits)
	displayLen    = len(Prefix) + 6
	touchInterval = time.Minute // last_used is written at most this often per token
)

// Errors returned by [Tokens.Verify].
var (
	ErrInvalid = errors.New("invalid API token")
	ErrExpired = errors.New("expired API token")
)

// Config is set by whoever builds the binary (or, under pocker, per instance).
type Config struct {
	// Collections are the auth collections (by name) whose records may own tokens. Default: _superusers only.
	Collections []string

	// MaxTTL caps a new token's lifetime (0: no cap; a token may never expire).
	MaxTTL time.Duration

	// Now is the clock (tests).
	Now func() time.Time
}

// Tokens is the token service of one app. Get it with [For].
type Tokens struct {
	app      core.App
	cfg      Config
	registry registry
}

//go:embed ui
var uiFiles embed.FS

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, cfg Config) *Tokens {
	t, err := Register(app, cfg)
	if err != nil {
		panic(err)
	}
	return t
}

// Register adds API tokens to app: the _tokens collection (on bootstrap), the bearer middleware, routes and
// dashboard screen (on serve), and on a *pocketbase.PocketBase the `token` command. Call it before app.Start(), or
// from a pocker plugin's InitApp.
func Register(app core.App, cfg Config) (*Tokens, error) {
	if len(cfg.Collections) == 0 {
		cfg.Collections = []string{core.CollectionNameSuperusers}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	t := &Tokens{app: app, cfg: cfg}
	app.Store().Set(storeKey, t)

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return EnsureSchema(e.App)
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.Bind(t.middleware())
		t.bindRoutes(se)
		ui, err := fs.Sub(uiFiles, "ui")
		if err != nil {
			return err
		}
		se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: "tokens", FS: ui})
		return se.Next()
	})

	// an owner is gone: so are its tokens
	app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().IsAuth() {
			if _, err := t.revokeWhere(dbx.HashExp{"owner_collection": e.Record.Collection().Id, "owner": e.Record.Id}); err != nil {
				e.App.Logger().Warn("tokens: revoke the deleted owner's tokens", "error", err)
			}
		}
		return e.Next()
	})

	app.Cron().MustAdd("__pbTokensCleanup__", "17 3 * * *", func() {
		if n, err := t.DeleteExpired(); err != nil {
			app.Logger().Warn("tokens: delete expired", "error", err)
		} else if n > 0 {
			app.Logger().Info("tokens: deleted expired tokens", "count", n)
		}
	})

	if pb, ok := app.(*pocketbase.PocketBase); ok {
		pb.RootCmd.AddCommand(NewCommand(t))
	}
	return t, nil
}

// For returns the token service registered on app, or nil.
func For(app core.App) *Tokens {
	t, _ := app.Store().Get(storeKey).(*Tokens)
	return t
}

// App returns the app this service belongs to.
func (t *Tokens) App() core.App { return t.app }

// RegisterScope adds a scope tokens can be limited to (see [Scope]). Registering a name again merges its paths.
func (t *Tokens) RegisterScope(s Scope) { t.registry.add(s) }

// Scopes returns the registered scopes, sorted by name.
func (t *Tokens) Scopes() []Scope { return t.registry.list() }

// AllowedCollection reports whether records of this auth collection may own tokens.
func (t *Tokens) AllowedCollection(c *core.Collection) bool {
	return c != nil && c.IsAuth() && slices.Contains(t.cfg.Collections, c.Name)
}

// CreateOptions describe a new token.
type CreateOptions struct {
	Name string
	// Scopes limit the token (registered names). Empty: the owner's full rights.
	Scopes []string
	// Expires is when the token stops working (zero: never, unless Config.MaxTTL caps it).
	Expires time.Time
	// ClientId is the OAuth client issuing it, if any.
	ClientId string
}

// Create issues a token for owner and returns its secret (shown once, never stored) and its record.
func (t *Tokens) Create(owner *core.Record, opts CreateOptions) (string, *core.Record, error) {
	if owner == nil || !t.AllowedCollection(owner.Collection()) {
		return "", nil, fmt.Errorf("tokens: records of this collection can't own API tokens")
	}
	scopes, err := t.normalizeScopes(owner.Collection(), opts.Scopes)
	if err != nil {
		return "", nil, err
	}
	now := t.cfg.Now()
	if t.cfg.MaxTTL > 0 {
		limit := now.Add(t.cfg.MaxTTL)
		if opts.Expires.IsZero() || opts.Expires.After(limit) {
			opts.Expires = limit
		}
	}
	if !opts.Expires.IsZero() && !opts.Expires.After(now) {
		return "", nil, fmt.Errorf("tokens: expiry is in the past")
	}

	col, err := t.app.FindCachedCollectionByNameOrId(CollectionId)
	if err != nil {
		return "", nil, err
	}
	secret := Prefix + security.RandomString(secretLen)
	rec := core.NewRecord(col)
	rec.Set("owner_collection", owner.Collection().Id)
	rec.Set("owner", owner.Id)
	rec.Set("name", strings.TrimSpace(opts.Name))
	rec.Set("hash", hashSecret(secret))
	rec.Set("prefix", secret[:displayLen])
	rec.Set("scopes", scopes)
	rec.Set("client_id", opts.ClientId)
	if !opts.Expires.IsZero() {
		rec.Set("expires", opts.Expires.UTC())
	}
	if err := t.app.Save(rec); err != nil {
		return "", nil, err
	}
	return secret, rec, nil
}

func (t *Tokens) normalizeScopes(owner *core.Collection, in []string) ([]string, error) {
	out := []string{}
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || slices.Contains(out, s) {
			continue
		}
		sc, ok := t.registry.get(s)
		if !ok {
			return nil, fmt.Errorf("tokens: unknown scope %q", s)
		}
		if len(sc.Collections) > 0 && !slices.Contains(sc.Collections, owner.Name) {
			return nil, fmt.Errorf("tokens: %s records can't hold the %q scope", owner.Name, s)
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out, nil
}

// Info is a verified token.
type Info struct {
	Token  *core.Record
	Owner  *core.Record
	Scopes []string // empty: the owner's full rights
}

// HasScope reports whether the token may act with scope: unscoped tokens have every scope.
func (i *Info) HasScope(scope string) bool {
	return len(i.Scopes) == 0 || slices.Contains(i.Scopes, scope)
}

// Verify resolves a secret to its token and owner. It doesn't record the use (the middleware does).
func (t *Tokens) Verify(secret string) (*Info, error) {
	if !strings.HasPrefix(secret, Prefix) || len(secret) != len(Prefix)+secretLen {
		return nil, ErrInvalid
	}
	rec, err := t.app.FindFirstRecordByData(CollectionId, "hash", hashSecret(secret))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalid
		}
		return nil, err
	}
	if exp := rec.GetDateTime("expires"); !exp.IsZero() && !exp.Time().After(t.cfg.Now()) {
		return nil, ErrExpired
	}
	col, err := t.app.FindCachedCollectionByNameOrId(rec.GetString("owner_collection"))
	if err != nil || !t.AllowedCollection(col) {
		return nil, ErrInvalid
	}
	owner, err := t.app.FindRecordById(col, rec.GetString("owner"))
	if err != nil {
		return nil, ErrInvalid
	}
	return &Info{Token: rec, Owner: owner, Scopes: scopesOf(rec)}, nil
}

func scopesOf(rec *core.Record) []string {
	var s []string
	_ = rec.UnmarshalJSONField("scopes", &s)
	return s
}

// touch records a use, at most once per touchInterval per token, without firing record hooks.
func (t *Tokens) touch(rec *core.Record, ip string) {
	now := t.cfg.Now()
	if last := rec.GetDateTime("last_used"); !last.IsZero() && now.Sub(last.Time()) < touchInterval && rec.GetString("last_ip") == ip {
		return
	}
	dt, _ := types.ParseDateTime(now.UTC())
	_, err := t.app.NonconcurrentDB().Update(Collection,
		dbx.Params{"last_used": dt.String(), "last_ip": ip},
		dbx.HashExp{"id": rec.Id}).Execute()
	if err != nil {
		t.app.Logger().Debug("tokens: record last use", "error", err)
	}
}

// List returns owner's tokens, newest first. A nil owner lists every token.
func (t *Tokens) List(owner *core.Record) ([]*core.Record, error) {
	q := t.app.RecordQuery(CollectionId).OrderBy("created DESC")
	if owner != nil {
		q = q.AndWhere(dbx.HashExp{"owner_collection": owner.Collection().Id, "owner": owner.Id})
	}
	var recs []*core.Record
	return recs, q.All(&recs)
}

// Get returns one token by id.
func (t *Tokens) Get(id string) (*core.Record, error) {
	return t.app.FindRecordById(CollectionId, id)
}

// Revoke deletes one token.
func (t *Tokens) Revoke(id string) error {
	rec, err := t.Get(id)
	if err != nil {
		return err
	}
	return t.app.Delete(rec)
}

// RevokeAll deletes every token of owner and returns how many.
func (t *Tokens) RevokeAll(owner *core.Record) (int, error) {
	return t.revokeWhere(dbx.HashExp{"owner_collection": owner.Collection().Id, "owner": owner.Id})
}

// DeleteExpired deletes tokens past their expiry and returns how many.
func (t *Tokens) DeleteExpired() (int, error) {
	dt, _ := types.ParseDateTime(t.cfg.Now().UTC())
	return t.revokeWhere(dbx.And(dbx.NewExp("expires != ''"), dbx.NewExp("expires <= {:now}", dbx.Params{"now": dt.String()})))
}

func (t *Tokens) revokeWhere(where dbx.Expression) (int, error) {
	var recs []*core.Record
	err := t.app.RecordQuery(CollectionId).AndWhere(where).All(&recs)
	if err != nil {
		return 0, err
	}
	n := 0
	err = t.app.RunInTransaction(func(tx core.App) error {
		for _, r := range recs {
			if err := tx.Delete(r); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
