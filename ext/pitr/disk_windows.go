//go:build windows

package pitr

// freeBytes is not implemented on Windows; the restore disk check is skipped there.
func freeBytes(dir string) (int64, bool) { return 0, false }
