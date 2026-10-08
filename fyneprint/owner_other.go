//go:build !windows

package fyneprint

// ownedByCaller is only a hazard on Windows: on macOS a dialog from the UI
// goroutine runs inline as a modal panel, and the portal dialog on
// Linux/BSD lives in another process.
func ownedByCaller(uintptr) bool { return false }
