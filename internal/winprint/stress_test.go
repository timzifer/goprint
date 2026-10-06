//go:build windows

package winprint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/testpdf"
)

// TestPrintToPDFConcurrent prints several jobs at once. Set
// GOPRINT_STRESS=<n> to run it.
func TestPrintToPDFConcurrent(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("GOPRINT_STRESS"))
	if n == 0 {
		t.Skip("set GOPRINT_STRESS")
	}
	requirePrinter(t, pdfPrinter)
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := filepath.Join(dir, fmt.Sprintf("%d.pdf", i))
			job, err := Print(ctx, bytes.NewReader(testpdf.Generate(i%3+1, 200, 200)), Options{Printer: pdfPrinter, Title: fmt.Sprint("stress ", i), OutputFile: out})
			if err != nil {
				t.Errorf("%d: Print: %v", i, err)
				return
			}
			if err := job.Wait(ctx); err != nil {
				t.Errorf("%d: Wait: %v", i, err)
				return
			}
			deadline := time.Now().Add(20 * time.Second)
			for {
				b, _ := os.ReadFile(out)
				if bytes.Contains(b, []byte("%%EOF")) {
					return
				}
				if time.Now().After(deadline) {
					t.Errorf("%d (job %s): output %d bytes after Wait", i, job.ID(), len(b))
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}
	wg.Wait()
}
