//go:build darwin

package macprint

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/timzifer/goprint/internal/errdefs"
	"github.com/timzifer/goprint/internal/testpdf"
)

// AppKit needs the main thread: keep the main goroutine on it and run the
// tests through RunMain.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	code := 0
	RunMain(func() { code = m.Run() })
	os.Exit(code)
}

func TestOnMain(t *testing.T) {
	if IsMainThread() {
		t.Fatal("test goroutine reports the main thread")
	}
	var onMain bool
	if err := OnMain(func() { onMain = IsMainThread() }); err != nil {
		t.Fatalf("OnMain: %v", err)
	}
	if !onMain {
		t.Error("OnMain did not run on the main thread")
	}
}

func TestOnMainWithoutLoop(t *testing.T) {
	loopMu.Lock()
	saved := loop
	loop = nil
	loopMu.Unlock()
	defer func() {
		loopMu.Lock()
		loop = saved
		loopMu.Unlock()
	}()
	called := false
	if err := OnMain(func() { called = true }); !errors.Is(err, errdefs.ErrWrongThread) {
		t.Fatalf("OnMain without RunMain = %v, want ErrWrongThread", err)
	}
	if called {
		t.Error("fn was called off the main thread")
	}
	if _, err := Dialog(testpdf.Generate(1, 100, 100), Options{}, false); !errors.Is(err, errdefs.ErrWrongThread) {
		t.Fatalf("Dialog off the main thread = %v, want ErrWrongThread", err)
	}
}

func TestRunMainOffMainThread(t *testing.T) {
	ran := false
	RunMain(func() { ran = true }) // not on the main thread: just calls f
	if !ran {
		t.Error("RunMain did not call f")
	}
}

func TestPrintToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.pdf")
	r, err := PrintToFile(testpdf.Generate(3, 200, 300), Options{
		Title: "macprint", PaperWidth: 595.28, PaperHeight: 841.89, Orientation: OrientationLandscape,
		SetOrientation: true, FirstPage: 2, Copies: 2, Collate: Yes, Duplex: DuplexLongEdge, Color: No,
		Scaling: ScaleToFit,
	}, out)
	if err != nil {
		t.Fatalf("PrintToFile: %v", err)
	}
	t.Logf("result: %+v", r)
	if r.Disposition != DispositionSave {
		t.Errorf("disposition %q, want %q", r.Disposition, DispositionSave)
	}
	if r.Orientation != OrientationLandscape || r.PaperWidth < r.PaperHeight {
		t.Errorf("paper %.1fx%.1f orientation %d, want landscape A4", r.PaperWidth, r.PaperHeight, r.Orientation)
	}
	if r.AllPages || r.FirstPage != 2 || r.LastPage != 3 {
		t.Errorf("pages all=%v %d-%d, want 2-3", r.AllPages, r.FirstPage, r.LastPage)
	}
	if r.Copies != 2 || r.Collate != Yes || r.Duplex != DuplexLongEdge {
		t.Errorf("copies %d collate %d duplex %d, want 2, yes, long edge", r.Copies, r.Collate, r.Duplex)
	}
	if r.ColorModel != "Gray" || r.PrintColorMode != "monochrome" {
		t.Errorf("color %q/%q, want Gray/monochrome", r.ColorModel, r.PrintColorMode)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Errorf("no output written: %v", err)
	}
}

func TestOnMainRunner(t *testing.T) {
	loopMu.Lock()
	saved := loop
	loop = nil
	loopMu.Unlock()
	defer func() {
		loopMu.Lock()
		loop = saved
		loopMu.Unlock()
		SetRunner(nil)
	}()

	// A toolkit's runner: hand the function to the main thread and wait,
	// here through the suspended RunMain loop.
	SetRunner(func(f func()) {
		done := make(chan struct{})
		saved.calls <- func() { defer close(done); f() }
		<-done
	})
	var onMain bool
	if err := OnMain(func() { onMain = IsMainThread() }); err != nil {
		t.Fatalf("OnMain with runner: %v", err)
	}
	if !onMain {
		t.Error("runner did not run fn on the main thread")
	}

	// A runner that stays on the calling goroutine must not run fn.
	SetRunner(func(f func()) { f() })
	called := false
	if err := OnMain(func() { called = true }); !errors.Is(err, errdefs.ErrWrongThread) {
		t.Fatalf("OnMain with wrong-thread runner = %v, want ErrWrongThread", err)
	}
	if called {
		t.Error("fn was called off the main thread")
	}
}
