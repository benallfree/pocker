package devusers

import (
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

func checkAccount(t *testing.T, app core.App, collection, email string) {
	t.Helper()
	rec, err := app.FindAuthRecordByEmail(collection, email)
	if err != nil || !rec.ValidatePassword(Password) || !rec.Verified() {
		t.Fatalf("%s %s missing, wrong password or unverified: %v", collection, email, err)
	}
}

func TestEnsure(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	if err := Ensure(app); err != nil {
		t.Fatal(err)
	}
	checkAccount(t, app, core.CollectionNameSuperusers, SuperuserEmail)
	checkAccount(t, app, UsersCollection, UserEmail)

	// changed passwords are put back
	for _, c := range [][2]string{{core.CollectionNameSuperusers, SuperuserEmail}, {UsersCollection, UserEmail}} {
		rec, _ := app.FindAuthRecordByEmail(c[0], c[1])
		rec.SetPassword("something-else")
		if err := app.Save(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := Ensure(app); err != nil {
		t.Fatal(err)
	}
	checkAccount(t, app, core.CollectionNameSuperusers, SuperuserEmail)
	checkAccount(t, app, UsersCollection, UserEmail)

	// a second call is a no-op
	if err := Ensure(app); err != nil {
		t.Fatal(err)
	}
}

func TestNoUsersCollection(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	users, err := app.FindCollectionByNameOrId(UsersCollection)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(users); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(app); err != nil {
		t.Fatal(err)
	}
	checkAccount(t, app, core.CollectionNameSuperusers, SuperuserEmail)
}

func TestRegisterOnBootstrap(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	Register(app)
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	checkAccount(t, app, core.CollectionNameSuperusers, SuperuserEmail)
	checkAccount(t, app, UsersCollection, UserEmail)
}
