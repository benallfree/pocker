// Package devusers gives local test builds one known account of each kind:
//
//   - superuser admin@example.com / password (the dashboard)
//   - user user@example.com / password, verified, in the "users" collection (the app itself)
//
// Every plugin's example/ registers it, so a fresh data dir is ready to log in to (no installer page, no CLI step)
// and the passwords are put back if someone changed them. Never register it in a production build.
package devusers

import (
	"database/sql"
	"errors"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// The local test accounts. Both use Password.
const (
	SuperuserEmail = "admin@example.com"
	UserEmail      = "user@example.com"
	Password       = "password"

	// UsersCollection is where the regular user goes. If the app has no such auth collection, it's skipped.
	UsersCollection = "users"
)

// Register makes app upsert the test accounts right after it bootstraps (system migrations done, before serve's
// installer check).
func Register(app core.App) {
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return Ensure(e.App)
	})
}

// Ensure creates the test accounts, or resets their passwords, on an already bootstrapped app. Under pocker, call
// it from SetupApp.
func Ensure(app core.App) error {
	if err := ensure(app, core.CollectionNameSuperusers, SuperuserEmail); err != nil {
		return err
	}
	col, err := app.FindCachedCollectionByNameOrId(UsersCollection)
	if err != nil || !col.IsAuth() {
		return nil // no users collection (yet): nothing to seed
	}
	return ensure(app, UsersCollection, UserEmail)
}

func ensure(app core.App, collection, email string) error {
	col, err := app.FindCachedCollectionByNameOrId(collection)
	if err != nil {
		return err
	}
	rec, err := app.FindAuthRecordByEmail(col, email)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		rec = core.NewRecord(col)
		rec.SetEmail(email)
	case err != nil:
		return err
	case rec.ValidatePassword(Password) && rec.Verified():
		return nil
	}
	rec.SetPassword(Password)
	rec.SetVerified(true)
	if err := app.Save(rec); err != nil {
		return err
	}
	app.Logger().Info("devusers: test account ready", "collection", collection, "email", email, "password", Password)
	return nil
}
