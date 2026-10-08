package goprint

// SetMainThreadRunner tells goprint how to run code on the process's main
// thread in programs whose main thread is owned by a GUI toolkit, so that
// [RunMain] cannot be used. run must execute its argument on the main
// thread and return after it returned, e.g. fyne.DoAndWait. nil removes
// the runner. The package [github.com/timzifer/goprint/fyneprint]
// installs one for Fyne.
//
// Only macOS needs the main thread (for the print panel); elsewhere run is
// never called. If run does not reach the main thread, Dialog returns
// [ErrWrongThread]. A RunMain loop, when active, takes precedence.
func SetMainThreadRunner(run func(f func())) {
	setMainThreadRunner(run)
}
