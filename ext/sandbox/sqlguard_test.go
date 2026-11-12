package sandbox

import (
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestAuthorizePolicy(t *testing.T) {
	dir := "/data/a/pb_data"
	cases := []struct {
		action     int32
		arg1, arg2 string
		want       int32
	}{
		{sqlite3.SQLITE_ATTACH, "", "", sqlite3.SQLITE_OK},                                  // temp db (plain VACUUM)
		{sqlite3.SQLITE_ATTACH, "/data/a/pb_data/copy.db", "", sqlite3.SQLITE_OK},           // VACUUM INTO own pb_data
		{sqlite3.SQLITE_ATTACH, "/data/b/pb_data/data.db", "", sqlite3.SQLITE_DENY},         // another app
		{sqlite3.SQLITE_ATTACH, "/data/a/pb_data/../../b/data.db", "", sqlite3.SQLITE_DENY}, // dot-dot
		{sqlite3.SQLITE_ATTACH, "file:/tmp/x.db?mode=rwc", "", sqlite3.SQLITE_DENY},
		{sqlite3.SQLITE_PRAGMA, "TEMP_STORE_DIRECTORY", "/tmp", sqlite3.SQLITE_DENY},
		{sqlite3.SQLITE_PRAGMA, "table_info", "users", sqlite3.SQLITE_OK},
		{sqlite3.SQLITE_FUNCTION, "", "load_extension", sqlite3.SQLITE_DENY},
		{sqlite3.SQLITE_FUNCTION, "", "lower", sqlite3.SQLITE_OK},
		{sqlite3.SQLITE_SELECT, "", "", sqlite3.SQLITE_OK},
	}
	for _, c := range cases {
		if got := authorize(c.action, c.arg1, c.arg2, dir); got != c.want {
			t.Errorf("authorize(%d, %q, %q) = %d, want %d", c.action, c.arg1, c.arg2, got, c.want)
		}
	}
}
