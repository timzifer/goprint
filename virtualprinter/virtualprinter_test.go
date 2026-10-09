package virtualprinter

import (
	"bytes"
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint"
)

var ctx = context.Background()

func doc() goprint.Document { return goprint.PDFBytes("Report", []byte("%PDF-1.7 test")) }

func client(p *Provider) *goprint.Client { return goprint.NewClient(p) }

func TestPrintersAndDefault(t *testing.T) {
	label := Label("Label")
	label.Default = true
	ps, err := client(New("v", Office("Office"), label)).Printers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Provider != "v" || ps[0].Default || !ps[1].Default || !ps[0].Caps.Duplex {
		t.Fatalf("Printers = %+v", ps)
	}
	// Without a marked default the first printer is the default.
	ps, _ = New("v", Office("A"), Label("B")).Printers(ctx)
	if !ps[0].Default || ps[1].Default {
		t.Errorf("implicit default: %+v", ps)
	}
}

func TestPrintKeepsPDF(t *testing.T) {
	vp := New("v", Office("Office"))
	job, err := client(vp).Print(ctx, doc(), goprint.Settings{Provider: "v", Copies: 2})
	if err != nil {
		t.Fatal(err)
	}
	jobs := vp.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("%d jobs", len(jobs))
	}
	j := jobs[0]
	if j.Printer != "Office" || j.Title != "Report" || string(j.PDF) != "%PDF-1.7 test" || j.Settings.Copies != 2 {
		t.Errorf("job = %+v", j)
	}
	if job.ID() != "1" || len(job.Warnings()) != 0 {
		t.Errorf("goprint job %q, warnings %v", job.ID(), job.Warnings())
	}
	if err := job.Wait(ctx); err != nil {
		t.Errorf("Wait = %v", err)
	}
}

func TestPrintImages(t *testing.T) {
	vp := New("v", Photo("Photo"))
	img := image.NewRGBA(image.Rect(0, 0, 30, 20))
	d := goprint.Document{Title: "img", Images: []image.Image{img, img}, DPI: 300}
	if _, err := client(vp).Print(ctx, d, goprint.Settings{Provider: "v"}); err != nil {
		t.Fatal(err)
	}
	pdf := vp.Jobs()[0].PDF
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || bytes.Count(pdf, []byte("/Type /Page ")) != 2 {
		t.Errorf("not a 2-page image PDF: %.40q", pdf)
	}
}

func TestWarningsAndStrict(t *testing.T) {
	vp := New("v", Label("Label"))
	s := goprint.Settings{
		Provider: "v",
		Media:    goprint.MediaA4,
		Duplex:   goprint.DuplexLongEdge,
		Color:    goprint.Color,
		Tray:     "tray-9",
		Quality:  goprint.QualityHigh,
		Vendor:   map[string]string{"b": "1", "a": "2"},
	}
	job, err := client(vp).Print(ctx, doc(), s)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range job.Warnings() {
		got = append(got, w.Setting)
	}
	want := "Media Duplex Color Tray Quality Vendor[a] Vendor[b]"
	if strings.Join(got, " ") != want {
		t.Errorf("warnings = %v, want %s", got, want)
	}

	s.Strict = true
	if _, err := client(vp).Print(ctx, doc(), s); !errors.Is(err, goprint.ErrUnsupported) {
		t.Errorf("strict = %v, want ErrUnsupported", err)
	}
	if n := len(vp.Jobs()); n != 1 {
		t.Errorf("strict failure created a job: %d jobs", n)
	}

	// What the printer offers draws no warning.
	ok := goprint.Settings{Provider: "v", Media: MediaLabel62x100, Duplex: goprint.DuplexNone, Color: goprint.Monochrome, Strict: true}
	if _, err := client(vp).Print(ctx, doc(), ok); err != nil {
		t.Errorf("supported settings: %v", err)
	}
}

func TestLookupErrors(t *testing.T) {
	c := client(New("v", Office("Office")))
	if _, err := c.Print(ctx, doc(), goprint.Settings{Provider: "v", Printer: "Nope"}); !errors.Is(err, goprint.ErrPrinterNotFound) {
		t.Errorf("unknown printer: %v", err)
	}
	if _, err := c.Capabilities(ctx, "v", "Nope"); !errors.Is(err, goprint.ErrPrinterNotFound) {
		t.Errorf("unknown printer caps: %v", err)
	}
	if _, err := client(New("v")).Print(ctx, doc(), goprint.Settings{Provider: "v"}); !errors.Is(err, goprint.ErrNoPrinter) {
		t.Errorf("no printers: %v", err)
	}
}

func TestScriptedFailures(t *testing.T) {
	vp := New("v", Office("Office"))
	c := client(vp)
	s := goprint.Settings{Provider: "v"}

	vp.FailNext(goprint.ErrBusy)
	vp.FailNext(errors.New("boom"))
	if _, err := c.Print(ctx, doc(), s); !errors.Is(err, goprint.ErrBusy) {
		t.Errorf("first = %v", err)
	}
	if _, err := c.Print(ctx, doc(), s); err == nil || err.Error() != "boom" {
		t.Errorf("second = %v", err)
	}
	if _, err := c.Print(ctx, doc(), s); err != nil {
		t.Errorf("third = %v", err)
	}

	vp.SetOffline("Office", true)
	if _, err := c.Print(ctx, doc(), s); !errors.Is(err, goprint.ErrBusy) {
		t.Errorf("offline = %v", err)
	}
	if ps, _ := c.Printers(ctx); len(ps) != 1 {
		t.Errorf("offline printer not listed: %v", ps)
	}
	vp.SetOffline("Office", false)
	if _, err := c.Print(ctx, doc(), s); err != nil {
		t.Errorf("online again = %v", err)
	}
	if n := len(vp.Jobs()); n != 2 {
		t.Errorf("%d jobs, want 2", n)
	}

	vp.Reset()
	if n := len(vp.Jobs()); n != 0 {
		t.Errorf("Reset kept %d jobs", n)
	}
}

func TestHeldJobs(t *testing.T) {
	vp := New("v", Office("Office"))
	vp.HoldJobs(true)
	job, err := client(vp).Print(ctx, doc(), goprint.Settings{Provider: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := job.State(ctx); st != goprint.JobPending {
		t.Fatalf("held job state = %v", st)
	}
	short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if err := job.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait on held job = %v", err)
	}

	j := vp.Jobs()[0]
	if err := j.SetState(goprint.JobProcessing); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { done <- job.Wait(ctx) }()
	if err := j.SetState(goprint.JobAborted); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Errorf("Wait after abort = %v", err)
	}
	if err := j.SetState(goprint.JobCompleted); err == nil {
		t.Error("left a final state")
	}
}

func TestCancel(t *testing.T) {
	vp := New("v", Office("Office"))
	vp.HoldJobs(true)
	job, _ := client(vp).Print(ctx, doc(), goprint.Settings{Provider: "v"})
	if err := job.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if err := job.Wait(ctx); !errors.Is(err, goprint.ErrCanceled) {
		t.Errorf("Wait = %v, want ErrCanceled", err)
	}
	if err := job.Cancel(ctx); err == nil {
		t.Error("canceled a canceled job")
	}
}

func TestLatency(t *testing.T) {
	p := Office("Slow")
	p.Latency = time.Hour
	vp := New("v", p)
	short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, err := client(vp).Print(short, doc(), goprint.Settings{Provider: "v"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Print = %v, want DeadlineExceeded", err)
	}
	if n := len(vp.Jobs()); n != 0 {
		t.Errorf("canceled Print created %d jobs", n)
	}
}

func TestAddRemovePrinter(t *testing.T) {
	vp := New("v", Office("Office"))
	vp.AddPrinter(Receipt("Till"))
	vp.AddPrinter(Label("Office")) // replaces
	ps, _ := vp.Printers(ctx)
	if len(ps) != 2 || ps[0].Caps.Duplex || ps[1].Name != "Till" {
		t.Fatalf("Printers = %+v", ps)
	}
	if !vp.RemovePrinter("Till") || vp.RemovePrinter("Till") {
		t.Error("RemovePrinter results")
	}
}

func TestPresetsValid(t *testing.T) {
	for _, p := range []Printer{Office("a"), Label("b"), Photo("c"), Receipt("d"), PDFWriter("e")} {
		for _, m := range p.Caps.Media {
			got, err := goprint.ParseMedia(m.Name)
			if err != nil || got != m {
				t.Errorf("%s: media %v parses as %v, %v", p.Description, m, got, err)
			}
		}
	}
	if !PDFWriter("x").ToFile {
		t.Error("PDFWriter not ToFile")
	}
}

func TestEmptyNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	New("")
}
