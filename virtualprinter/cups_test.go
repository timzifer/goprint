package virtualprinter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint"
)

// TestCUPSPrintsToServer adds the server as an IPP Everywhere queue to the
// local CUPS and prints to it with lp: a client goprint did not write.
// Set GOPRINT_CUPS_VIRTUAL=1 and GOPRINT_LPADMIN to the lpadmin command,
// e.g. "sudo -n lpadmin".
func TestCUPSPrintsToServer(t *testing.T) {
	if os.Getenv("GOPRINT_CUPS_VIRTUAL") == "" {
		t.Skip("set GOPRINT_CUPS_VIRTUAL=1 to test against the local CUPS")
	}
	lpadmin := strings.Fields(os.Getenv("GOPRINT_LPADMIN"))
	if len(lpadmin) == 0 {
		lpadmin = []string{"lpadmin"}
	}
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		t.Logf("%s: %s", strings.Join(args, " "), out)
		if err != nil {
			t.Fatalf("%s: %v", args[0], err)
		}
	}

	vp := New("v", Office("Office"))
	srv := serve(t, vp)
	const queue = "goprint-virtual"
	run(append(lpadmin, "-p", queue, "-E", "-v", srv.PrinterURI("Office"), "-m", "everywhere")...)
	t.Cleanup(func() { _ = exec.Command(lpadmin[0], append(lpadmin[1:], "-x", queue)...).Run() })

	file := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(file, a4PDF(), 0o644); err != nil {
		t.Fatal(err)
	}
	run("lp", "-d", queue, "-t", "From CUPS", "-n", "2", "-o", "sides=two-sided-long-edge", file)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for len(vp.Jobs()) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("CUPS sent no job")
		case <-time.After(200 * time.Millisecond):
		}
	}
	j := vp.Jobs()[0]
	t.Logf("job %q: %d bytes, settings %+v, warnings %v", j.Title, len(j.PDF), j.Settings, j.Warnings)
	if !strings.HasPrefix(string(j.PDF), "%PDF") {
		t.Errorf("not a PDF: %.16q", j.PDF)
	}
	if j.Settings.Duplex != goprint.DuplexLongEdge {
		t.Errorf("duplex %v", j.Settings.Duplex)
	}
	// CUPS either sends the copies or makes them itself.
	if j.Settings.Copies != 2 && j.Settings.Copies != 0 {
		t.Errorf("copies %d", j.Settings.Copies)
	}
}
