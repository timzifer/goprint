package fyneprint

import "syscall"

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
)

// ownedByCaller reports whether hwnd belongs to the calling thread. Fyne's
// UI goroutine is locked to the thread that owns its windows; a dialog
// started there would wait for a window whose thread waits for the dialog.
func ownedByCaller(hwnd uintptr) bool {
	owner, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	self, _, _ := procGetCurrentThreadId.Call()
	return owner != 0 && owner == self
}
