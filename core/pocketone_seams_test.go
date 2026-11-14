package core_test

// PocketOne: tests for the core seams ext/ relies on (BaseAppConfig.AppMigrations and OnLog), so an upstream merge that
// breaks one fails here and not only in ext/.

import (
	"strings"
	"sync"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/logger"
)

func TestAppMigrationsOf(t *testing.T) {
	plain, _ := tests.NewTestApp()
	defer plain.Cleanup()
	if core.AppMigrationsOf(plain) != &core.AppMigrations {
		t.Fatal("an app without its own list should use the global AppMigrations")
	}

	own := &core.MigrationsList{}
	app, err := tests.NewTestAppWithConfig(core.BaseAppConfig{AppMigrations: own})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	if core.AppMigrationsOf(app) != own {
		t.Fatal("AppMigrationsOf should return the configured list (also through an embedding *BaseApp)")
	}
	type appWrapper struct{ core.App }
	if core.AppMigrationsOf(appWrapper{app}) != own {
		t.Fatal("AppMigrationsOf should see through a wrapper that embeds the App interface")
	}

	globalBefore := len(core.AppMigrations.Items())
	ran := 0
	own.Register(func(txApp core.App) error { ran++; return nil }, nil, "1700000000_ce_seam_test.go")
	if err := app.RunAppMigrations(); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Fatalf("RunAppMigrations ran the app's own migration %d times, want 1", ran)
	}
	if len(core.AppMigrations.Items()) != globalBefore {
		t.Fatal("registering on the app's own list changed the global list")
	}
}

func TestOnLog(t *testing.T) {
	var mu sync.Mutex
	var got []string
	app, err := tests.NewTestAppWithConfig(core.BaseAppConfig{
		OnLog: func(l *logger.Log) {
			mu.Lock()
			got = append(got, l.Message)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	app.Logger().Info("ce seam test entry")

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(got, "\n"), "ce seam test entry") {
		t.Fatalf("OnLog didn't receive the entry, got %q", got)
	}
}
