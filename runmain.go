package goprint

// RunMain runs f and, while f runs, lets [Dialog] use the calling thread
// for the native UI. It returns when f returns.
//
// On macOS, AppKit (and so the print panel) works only on the process's
// main thread. A program that shows dialogs locks its main goroutine to
// the main thread in an init function of package main and runs its work
// through RunMain:
//
//	func init() { runtime.LockOSThread() }
//
//	func main() {
//		goprint.RunMain(func() {
//			// Dialog may be called from any goroutine in here.
//		})
//	}
//
// Alternatively Dialog can be called directly from the (locked) main
// goroutine. Called from another thread without RunMain, Dialog returns
// [ErrWrongThread]. Programs that already run a Cocoa event loop on the
// main thread (e.g. a GUI toolkit) call Dialog from that thread instead.
//
// On other platforms RunMain just calls f.
func RunMain(f func()) {
	runMain(f)
}
