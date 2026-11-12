//go:build windows

package cmd

// lockChangelog is a no-op on Windows, like the store's lock.
func lockChangelog(string) (func(), error) { return func() {}, nil }
