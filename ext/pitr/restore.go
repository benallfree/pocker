package pitr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/benbjohnson/litestream"
)

// File names in pb_data/litestream/.
const (
	pendingDBName   = "pending.db"   // a restored database waiting to be swapped in on the next bootstrap
	pendingName     = "pending.json" // marks pending.db as complete
	beforeName      = "before-restore.db"
	lastRestoreName = "last-restore.json"
)

// LastRestore is the outcome of the most recent restore, kept outside data.db so a restore can't overwrite it.
type LastRestore struct {
	At         time.Time `json:"at"`         // the moment the database was put back to
	RestoredAt time.Time `json:"restoredAt"` // when it was swapped in; restoring to just before this undoes it
	Error      string    `json:"error,omitempty"`
}

type pending struct {
	At       time.Time `json:"at"`
	StagedAt time.Time `json:"stagedAt"`
}

// Window is the range of moments a restore can go back to.
type Window struct {
	Earliest time.Time `json:"earliest"`
	Latest   time.Time `json:"latest"`
}

// Status is what the admin section shows.
type Status struct {
	Enabled     bool         `json:"enabled"`
	Retention   string       `json:"retention"`
	Destination string       `json:"destination"` // "s3" (the app's own bucket), "storage" (the server's) or "local"
	Label       string       `json:"label,omitempty"`
	Window      *Window      `json:"window,omitempty"` // nil until the first snapshot is written
	Size        int64        `json:"size"`             // bytes of history
	LastRestore *LastRestore `json:"lastRestore,omitempty"`
	Restoring   bool         `json:"restoring"`
	Pending     *time.Time   `json:"pending,omitempty"`
}

// replica returns a database-less replica for reading the history.
func (a *appPITR) replica() (*litestream.Replica, *destination, error) {
	dest, err := a.destination()
	if err != nil {
		return nil, nil, err
	}
	if err := dest.client.Init(context.Background()); err != nil {
		dest.close()
		return nil, nil, err
	}
	return litestream.NewReplicaWithClient(nil, dest.client), dest, nil
}

// Status reports the restorable window and history size.
func (a *appPITR) Status(ctx context.Context) (*Status, error) {
	st := &Status{Enabled: true, Retention: a.retention.String()}
	r, dest, err := a.replica()
	if err != nil {
		return nil, err
	}
	defer dest.close()
	st.Destination, st.Label = dest.kind, dest.label
	created, updated, err := r.TimeBounds(ctx)
	if err == nil && !created.IsZero() {
		st.Window = &Window{Earliest: created, Latest: updated}
	}
	for lvl := 0; lvl <= litestream.SnapshotLevel; lvl++ {
		it, err := r.Client.LTXFiles(ctx, lvl, 0, false)
		if err != nil {
			continue
		}
		for it.Next() {
			st.Size += it.Item().Size
		}
		_ = it.Close()
	}
	st.LastRestore = readLastRestore(a.dataDir())
	a.mu.Lock()
	st.Restoring = a.restoring
	a.mu.Unlock()
	if p, err := readPending(a.dataDir()); err == nil {
		st.Pending = &p.At
	}
	return st, nil
}

// Stage restores the history at `at` into pb_data/litestream/pending.db while the app keeps serving, and marks it to
// be swapped in on the next bootstrap. Nothing touches the live database.
func (a *appPITR) Stage(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		return errors.New("a time to restore to is required")
	}
	if at.After(time.Now()) {
		return errors.New("that time is in the future")
	}
	if err := a.sync(ctx); err != nil { // include the newest changes
		return fmt.Errorf("flush before restore: %w", err)
	}
	r, dest, err := a.replica()
	if err != nil {
		return err
	}
	defer dest.close()
	created, _, err := r.TimeBounds(ctx)
	if err != nil {
		return err
	}
	if created.IsZero() {
		return errors.New("no history yet")
	}
	if at.Before(created) {
		return fmt.Errorf("history starts at %s", created.UTC().Format(time.RFC3339))
	}
	if err := checkDisk(a.dataDir()); err != nil {
		return err
	}
	dir := a.stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, pendingName))
	out := filepath.Join(dir, pendingDBName)
	for _, f := range []string{out, out + "-wal", out + "-shm", out + "-txid"} {
		_ = os.Remove(f)
	}
	opt := litestream.NewRestoreOptions()
	opt.OutputPath = out
	opt.Timestamp = at
	opt.IntegrityCheck = litestream.IntegrityCheckQuick
	if err := r.Restore(ctx, opt); err != nil {
		_ = os.Remove(out)
		return fmt.Errorf("restore: %w", err)
	}
	return writeJSON(filepath.Join(dir, pendingName), pending{At: at, StagedAt: time.Now()})
}

// Restore stages a restore to `at` and restarts the app, which swaps it in while starting up. The restart happens
// in the background after a short delay, so a request that asked for it can still be answered.
func (a *appPITR) Restore(ctx context.Context, at time.Time) error {
	a.mu.Lock()
	if a.restoring {
		a.mu.Unlock()
		return errors.New("a restore is already running")
	}
	a.restoring = true
	a.mu.Unlock()
	if err := a.Stage(ctx, at); err != nil {
		a.mu.Lock()
		a.restoring = false
		a.mu.Unlock()
		return err
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.logger().Info("pitr: restarting to restore", "at", at)
		if err := a.app.Restart(); err != nil {
			a.logger().Error("pitr: restart failed; the restore will apply on the next start", "error", err)
		}
		a.mu.Lock()
		a.restoring = false
		a.mu.Unlock()
	}()
	return nil
}

// applyPending swaps a staged restore in, before the app opens its database. It keeps the current database as
// before-restore.db (only the latest one).
func applyPending(dataDir string) (swapped bool, err error) {
	dir := filepath.Join(dataDir, DirName)
	p, err := readPending(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		_ = os.Remove(filepath.Join(dir, pendingName))
		return false, err
	}
	staged := filepath.Join(dir, pendingDBName)
	if _, err := os.Stat(staged); err != nil {
		_ = os.Remove(filepath.Join(dir, pendingName))
		_ = writeJSON(filepath.Join(dir, lastRestoreName), LastRestore{At: p.At, RestoredAt: time.Now(), Error: "staged database missing"})
		return false, err
	}
	db := filepath.Join(dataDir, "data.db")
	before := filepath.Join(dir, beforeName)
	for _, f := range []string{before, before + "-wal", before + "-shm"} {
		_ = os.Remove(f)
	}
	if err := os.Rename(db, before); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.Rename(db+"-wal", before+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	_ = os.Remove(db + "-shm")
	if err := os.Rename(staged, db); err != nil {
		_ = os.Rename(before, db)
		_ = os.Rename(before+"-wal", db+"-wal")
		return false, err
	}
	for _, f := range []string{staged + "-wal", staged + "-shm", staged + "-txid", filepath.Join(dir, pendingName)} {
		_ = os.Remove(f)
	}
	return true, writeJSON(filepath.Join(dir, lastRestoreName), LastRestore{At: p.At, RestoredAt: time.Now()})
}

// rollback puts before-restore.db back after a restored database failed to open.
func rollback(dataDir string, cause error) error {
	dir := filepath.Join(dataDir, DirName)
	db := filepath.Join(dataDir, "data.db")
	before := filepath.Join(dir, beforeName)
	for _, f := range []string{db, db + "-wal", db + "-shm"} {
		_ = os.Remove(f)
	}
	if err := os.Rename(before, db); err != nil {
		return err
	}
	if err := os.Rename(before+"-wal", db+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	last := readLastRestore(dataDir)
	if last == nil {
		last = &LastRestore{RestoredAt: time.Now()}
	}
	last.Error = "the restored database did not open, so the previous one was put back: " + cause.Error()
	return writeJSON(filepath.Join(dir, lastRestoreName), last)
}

func readPending(dataDir string) (*pending, error) {
	var p pending
	raw, err := os.ReadFile(filepath.Join(dataDir, DirName, pendingName))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func readLastRestore(dataDir string) *LastRestore {
	raw, err := os.ReadFile(filepath.Join(dataDir, DirName, lastRestoreName))
	if err != nil {
		return nil
	}
	var l LastRestore
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	return &l
}

func writeJSON(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// checkDisk refuses a restore when free space is under twice the current database size (the staged copy plus the
// kept one).
func checkDisk(dataDir string) error {
	fi, err := os.Stat(filepath.Join(dataDir, "data.db"))
	if err != nil {
		return nil
	}
	free, ok := freeBytes(dataDir)
	if !ok {
		return nil
	}
	if need := 2 * fi.Size(); free < need {
		return fmt.Errorf("not enough free disk space for a restore: need %d MiB, have %d MiB", need>>20, free>>20)
	}
	return nil
}

// restoreTo writes the database as it was at `at` into out (side restore; the app is untouched).
func restoreTo(ctx context.Context, a *appPITR, at time.Time, out string) error {
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", out)
	}
	r, dest, err := a.replica()
	if err != nil {
		return err
	}
	defer dest.close()
	opt := litestream.NewRestoreOptions()
	opt.OutputPath = out
	opt.Timestamp = at
	opt.IntegrityCheck = litestream.IntegrityCheckQuick
	return r.Restore(ctx, opt)
}
