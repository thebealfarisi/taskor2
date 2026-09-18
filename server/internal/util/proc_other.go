//go:build !windows

package util

// EnsureHiddenConsole is a no-op on non-Windows platforms.
func EnsureHiddenConsole() {
	// no-op on non-Windows platforms: console window hiding is Windows-specific.
}
