//go:build windows

package winprint

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// Print-to-file jobs from separate processes sometimes lose their output
// when they run at the same time ("Microsoft Print to PDF" completes the
// second job with an empty file). goprint serializes its own print-to-file
// jobs machine-wide with a lock file: LockFileEx locks belong to the file
// handle (not a thread) and are released when the process exits, so a
// crashed process cannot block others.

const lockWait = 2 * time.Minute

// printToFileLock acquires the machine-wide print-to-file lock. It gives up
// (returning a no-op release) after lockWait or when ctx is done, so a
// stuck holder delays but never blocks printing.
func printToFileLock(ctx context.Context) (release func()) {
	path := filepath.Join(os.TempDir(), "goprint-print-to-file.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		tracef("print-to-file lock: %v", err)
		return func() {}
	}
	h := windows.Handle(f.Fd())
	deadline := time.Now().Add(lockWait)
	for {
		ol := new(windows.Overlapped)
		err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
		if err == nil {
			tracef("print-to-file lock acquired")
			return func() {
				_ = windows.UnlockFileEx(h, 0, 1, 0, new(windows.Overlapped))
				f.Close()
				tracef("print-to-file lock released")
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			tracef("print-to-file lock not acquired: %v", err)
			f.Close()
			return func() {}
		}
		time.Sleep(50 * time.Millisecond)
	}
}
