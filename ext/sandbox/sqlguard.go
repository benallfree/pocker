package sandbox

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// The SQL guard keeps an app's SQL inside its own database files, using SQLite's authorizer (sqlite3_set_authorizer):
// SQLite reports each action after parsing, so nothing depends on matching statement text.
//
// PocketBase lets JS hooks and migrations run raw SQL ($app.db().newQuery(...)). Plain SQL only touches the
// connection's own database; the ways out are:
//
//   - ATTACH (also used internally by VACUUM and VACUUM INTO): allowed only for SQLite's temporary database or a file
//     inside the app's own pb_data (PocketBase's backups VACUUM INTO pb_data);
//   - PRAGMA temp_store_directory / data_store_directory: process-wide settings, refused;
//   - load_extension(): refused (also off by default).
//
// modernc's driver doesn't expose the authorizer, so it is installed on the driver's connection handle directly, the
// same way the driver installs its own callbacks. If the driver's internals change, opening the database fails
// (fail closed) and the tests flag it.

const guardDriverName = "sandbox-sqlite"

var registerGuard sync.Once

// queryLimits maps an app's pb_data folder to the deadline for its statements that have none of their own (raw SQL
// from JS has none), so a runaway query (e.g. an endless recursive CTE) is interrupted inside SQLite.
var queryLimits sync.Map // string -> time.Duration

// GuardedDBConnect returns a core.DBConnectFunc like core.DefaultDBConnect, opening through the SQL guard, with
// maxQuery as the deadline for statements that have none (0 = none).
func GuardedDBConnect(maxQuery time.Duration) core.DBConnectFunc {
	registerGuard.Do(func() {
		base, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			panic(err)
		}
		inner := base.Driver()
		_ = base.Close()
		sql.Register(guardDriverName, &guardDriver{inner: inner})
	})
	return func(dbPath string) (*dbx.DB, error) {
		if dir, err := filepath.Abs(filepath.Dir(dbPath)); err == nil {
			queryLimits.Store(dir, maxQuery)
		}
		// same pragmas as core.DefaultDBConnect
		pragmas := "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=journal_size_limit(200000000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-32000)&_defensive=1"
		return dbx.Open(guardDriverName, dbPath+pragmas)
	}
}

type guardDriver struct{ inner driver.Driver }

func (d *guardDriver) Open(dsn string) (driver.Conn, error) {
	c, err := d.inner.Open(dsn)
	if err != nil {
		return nil, err
	}
	path := dsn
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	allowed, err := filepath.Abs(filepath.Dir(strings.TrimPrefix(path, "file:")))
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	handle, err := installAuthorizer(c, allowed)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("pocker: SQL guard: %w", err)
	}
	var maxQuery time.Duration
	if v, ok := queryLimits.Load(allowed); ok {
		maxQuery = v.(time.Duration)
	}
	return &guardConn{inner: c, handle: handle, maxQuery: maxQuery}, nil
}

// authorizers maps a connection's sqlite3* handle to the app folder its SQL may touch.
var authorizers = struct {
	sync.RWMutex
	m map[uintptr]string
}{m: map[uintptr]string{}}

// installAuthorizer reads the driver connection's sqlite3* handle and TLS and registers authorize on it.
func installAuthorizer(c driver.Conn, allowedDir string) (uintptr, error) {
	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return 0, errors.New("unexpected sqlite connection type")
	}
	v = v.Elem()
	dbF, tlsF := v.FieldByName("db"), v.FieldByName("tls")
	if !dbF.IsValid() || dbF.Kind() != reflect.Uintptr || !tlsF.IsValid() || tlsF.Type() != reflect.TypeOf((*libc.TLS)(nil)) {
		return 0, errors.New("sqlite driver internals changed (db/tls fields)")
	}
	db := *(*uintptr)(unsafe.Pointer(dbF.UnsafeAddr()))
	tls := *(**libc.TLS)(unsafe.Pointer(tlsF.UnsafeAddr()))
	if db == 0 || tls == nil {
		return 0, errors.New("sqlite connection not open")
	}

	authorizers.Lock()
	authorizers.m[db] = allowedDir
	authorizers.Unlock()
	if rc := sqlite3.Xsqlite3_set_authorizer(tls, db, cFuncPointer(authorizeTrampoline), db); rc != sqlite3.SQLITE_OK {
		authorizers.Lock()
		delete(authorizers.m, db)
		authorizers.Unlock()
		return 0, fmt.Errorf("sqlite3_set_authorizer: rc=%d", rc)
	}
	return db, nil
}

// authorizeTrampoline is the C callback: int xAuth(void*, int action, const char*, const char*, const char*, const char*).
func authorizeTrampoline(tls *libc.TLS, handle uintptr, action int32, z1, z2, z3, z4 uintptr) int32 {
	authorizers.RLock()
	dir, ok := authorizers.m[handle]
	authorizers.RUnlock()
	if !ok {
		return sqlite3.SQLITE_DENY
	}
	return authorize(action, libc.GoString(z1), libc.GoString(z2), dir)
}

// authorize is the policy, separate from the C plumbing so it can be tested directly.
func authorize(action int32, arg1, arg2, allowedDir string) int32 {
	switch action {
	case sqlite3.SQLITE_ATTACH:
		// "" is SQLite's temporary database (plain VACUUM attaches one internally)
		if arg1 == "" || insideDir(arg1, allowedDir) {
			return sqlite3.SQLITE_OK
		}
		return sqlite3.SQLITE_DENY
	case sqlite3.SQLITE_PRAGMA:
		switch strings.ToLower(arg1) {
		case "temp_store_directory", "data_store_directory":
			return sqlite3.SQLITE_DENY
		}
	case sqlite3.SQLITE_FUNCTION:
		if strings.EqualFold(arg2, "load_extension") {
			return sqlite3.SQLITE_DENY
		}
	}
	return sqlite3.SQLITE_OK
}

// cFuncPointer converts a top-level Go function to a C function pointer, like modernc's driver does internally.
func cFuncPointer[T any](f T) uintptr {
	return *(*uintptr)(unsafe.Pointer(&struct{ f T }{f}))
}

// guardConn passes everything through to the driver; it exists to drop the authorizer entry on Close.
type guardConn struct {
	inner    driver.Conn
	handle   uintptr
	maxQuery time.Duration
}

func (c *guardConn) Prepare(query string) (driver.Stmt, error) {
	st, err := c.inner.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &guardStmt{inner: st, maxQuery: c.maxQuery}, nil
}

func (c *guardConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var st driver.Stmt
	var err error
	if p, ok := c.inner.(driver.ConnPrepareContext); ok {
		st, err = p.PrepareContext(ctx, query)
	} else {
		st, err = c.inner.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &guardStmt{inner: st, maxQuery: c.maxQuery}, nil
}

func (c *guardConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.inner.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	ctx, cancel := withQueryDeadline(ctx, c.maxQuery)
	defer cancel()
	return e.ExecContext(ctx, query, args)
}

func (c *guardConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.inner.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	ctx, cancel := withQueryDeadline(ctx, c.maxQuery)
	rows, err := q.QueryContext(ctx, query, args)
	if err != nil {
		cancel()
		return nil, err
	}
	return &guardRows{Rows: rows, cancel: cancel}, nil
}

// withQueryDeadline adds max to a context that has no deadline.
func withQueryDeadline(ctx context.Context, max time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || max <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, max)
}

// guardRows releases the query deadline when the rows are closed (they stream after QueryContext returns).
type guardRows struct {
	driver.Rows
	cancel context.CancelFunc
}

func (r *guardRows) Close() error {
	err := r.Rows.Close()
	r.cancel()
	return err
}

// guardStmt applies the query deadline to prepared statements.
type guardStmt struct {
	inner    driver.Stmt
	maxQuery time.Duration
}

func (s *guardStmt) Close() error  { return s.inner.Close() }
func (s *guardStmt) NumInput() int { return s.inner.NumInput() }

func (s *guardStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamed(args))
}

func (s *guardStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamed(args))
}

func (s *guardStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	ctx, cancel := withQueryDeadline(ctx, s.maxQuery)
	defer cancel()
	if e, ok := s.inner.(driver.StmtExecContext); ok {
		return e.ExecContext(ctx, args)
	}
	return s.inner.Exec(namedToValues(args)) //nolint:staticcheck
}

func (s *guardStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	ctx, cancel := withQueryDeadline(ctx, s.maxQuery)
	var rows driver.Rows
	var err error
	if q, ok := s.inner.(driver.StmtQueryContext); ok {
		rows, err = q.QueryContext(ctx, args)
	} else {
		rows, err = s.inner.Query(namedToValues(args)) //nolint:staticcheck
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return &guardRows{Rows: rows, cancel: cancel}, nil
}

func valuesToNamed(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}

func namedToValues(args []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

func (c *guardConn) Close() error {
	err := c.inner.Close()
	authorizers.Lock()
	delete(authorizers.m, c.handle)
	authorizers.Unlock()
	return err
}

func (c *guardConn) Begin() (driver.Tx, error) {
	return c.inner.Begin() //nolint:staticcheck // delegated as-is
}

func (c *guardConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.inner.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.inner.Begin() //nolint:staticcheck
}

func (c *guardConn) Ping(ctx context.Context) error {
	if p, ok := c.inner.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *guardConn) ResetSession(ctx context.Context) error {
	if r, ok := c.inner.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *guardConn) IsValid() bool {
	if v, ok := c.inner.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func insideDir(p, dir string) bool {
	if dir == "" {
		return false
	}
	p = strings.TrimPrefix(p, "file:")
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	r, err := filepath.Rel(dir, abs)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
