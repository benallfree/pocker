package pitr

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
)

// MustRegister adds point-in-time restore to a single PocketBase app (call before app.Start) and the `pitr`
// command. It panics on error, like PocketBase's own plugins.
func MustRegister(app *pocketbase.PocketBase, cfg Config) *Plugin {
	p, err := Register(app, cfg)
	if err != nil {
		panic(err)
	}
	return p
}

// Register is [MustRegister] returning the error.
func Register(app *pocketbase.PocketBase, cfg Config) (*Plugin, error) {
	p := New(cfg)
	if err := p.SetupApp(app, AppConfig{}); err != nil {
		return nil, err
	}
	// close the stores after the app's own terminate (and pitr's detach) has run
	app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{
		Priority: -1000,
		Func: func(e *core.TerminateEvent) error {
			err := e.Next()
			if !e.IsRestart {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = p.Close(ctx)
			}
			return err
		},
	})
	app.RootCmd.AddCommand(p.Command(app))
	return p, nil
}

// Command returns the `pitr` command for a single app: status, and restore into a file or (server stopped) in place.
func (p *Plugin) Command(app core.App) *cobra.Command {
	cmd := &cobra.Command{Use: Name, Short: "Point-in-time restore of the database"}
	a := func() *appPITR {
		_, retention := p.resolve(AppConfig{})
		return &appPITR{p: p, app: app, retention: retention}
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the restorable window and history size",
		RunE: func(c *cobra.Command, _ []string) error {
			st, err := a().Status(c.Context())
			if err != nil {
				return err
			}
			if st.Window == nil {
				fmt.Println("No history yet.")
			} else {
				fmt.Printf("Restorable: %s – %s\n", st.Window.Earliest.Local().Format(time.RFC3339), st.Window.Latest.Local().Format(time.RFC3339))
			}
			fmt.Printf("Destination: %s %s\nHistory size: %d KiB\nRetention: %s\n", st.Destination, st.Label, st.Size/1024, st.Retention)
			if l := st.LastRestore; l != nil {
				fmt.Printf("Last restore: to %s at %s %s\n", l.At.Local().Format(time.RFC3339), l.RestoredAt.Local().Format(time.RFC3339), l.Error)
			}
			return nil
		},
	})
	var at, to string
	restore := &cobra.Command{
		Use:   "restore",
		Short: "Restore the database as it was at --at, into --to, or in place on the next start",
		Long: "With --to, writes the database as it was at --at into that file (any time, also on a fresh machine with " +
			"the same bucket config). Without --to, stages the restore in place; run it while the server is stopped: " +
			"the swap happens on the next start.",
		RunE: func(c *cobra.Command, _ []string) error {
			t, err := time.Parse(time.RFC3339, at)
			if err != nil {
				return fmt.Errorf("--at: expected RFC 3339 (e.g. 2026-11-11T14:05:00Z): %w", err)
			}
			if to != "" {
				return restoreTo(c.Context(), a(), t, to)
			}
			if err := a().Stage(c.Context(), t); err != nil {
				return err
			}
			fmt.Println("Staged. The database is swapped in on the next start (the current one is kept in pb_data/litestream/before-restore.db).")
			return nil
		},
	}
	restore.Flags().StringVar(&at, "at", "", "time to restore to (RFC 3339)")
	restore.Flags().StringVar(&to, "to", "", "write into this file instead of restoring in place")
	_ = restore.MarkFlagRequired("at")
	cmd.AddCommand(restore)
	return cmd
}
