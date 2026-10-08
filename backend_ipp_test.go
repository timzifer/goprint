package goprint

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/testpdf"
	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

func mockBackend(t *testing.T, printers ...ipptest.Printer) (ippBackend, *ipptest.Server) {
	t.Helper()
	srv := ipptest.NewServer(printers...)
	t.Cleanup(srv.Close)
	return ippBackend{newClient: func(opts ...ipp.Option) (*ipp.Client, error) {
		return ipp.NewClient(srv.URL, append(opts, ipp.WithUserName("tester"))...)
	}}, srv
}

var officeAttrs = ipp.Attributes{
	{Name: "printer-info", Values: []ipp.Value{ipp.Text("Office printer")}},
	{Name: "printer-location", Values: []ipp.Value{ipp.Text("2nd floor")}},
	{Name: "media-supported", Values: []ipp.Value{ipp.Keyword("iso_a4_210x297mm"), ipp.Keyword("na_letter_8.5x11in"), ipp.Keyword("custom_min_10x10mm")}},
	{Name: "sides-supported", Values: []ipp.Value{ipp.Keyword("one-sided"), ipp.Keyword("two-sided-long-edge")}},
	{Name: "print-color-mode-supported", Values: []ipp.Value{ipp.Keyword("monochrome"), ipp.Keyword("color")}},
	{Name: "printer-resolution-supported", Values: []ipp.Value{ipp.Resolution{X: 600, Y: 600, Units: ipp.UnitsDPI}, ipp.Resolution{X: 472, Y: 472, Units: ipp.UnitsDPCM}}},
	{Name: "document-format-supported", Values: []ipp.Value{ipp.MimeMediaType("application/pdf")}},
	{Name: "media-source-supported", Values: []ipp.Value{ipp.Keyword("auto"), ipp.Keyword("tray-1"), ipp.Keyword("manual")}},
	{Name: "print-quality-supported", Values: []ipp.Value{ipp.Enum(3), ipp.Enum(4), ipp.Enum(5)}},
}

func pdfDoc(title string) Document {
	b := testpdf.Generate(2, testpdf.A4Width, testpdf.A4Height)
	return Document{Title: title, PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(b)}, nil }}
}

func TestIPPPrinters(t *testing.T) {
	b, _ := mockBackend(t,
		ipptest.Printer{Name: "Office", Default: true, Attrs: officeAttrs},
		ipptest.Printer{Name: "Lab"},
	)
	ps, err := b.printers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d printers", len(ps))
	}
	byName := map[string]Printer{}
	for _, p := range ps {
		byName[p.Name] = p
	}
	o := byName["Office"]
	if !o.Default || byName["Lab"].Default || o.Description != "Office printer" || o.Location != "2nd floor" {
		t.Errorf("Office = %+v", o)
	}
	want := Capabilities{
		Media:       []Media{MediaA4, MediaLetter, {Name: "custom_min_10x10mm", Width: 10000, Height: 10000}},
		Duplex:      true,
		Color:       true,
		Resolutions: []Resolution{{600, 600}, {1199, 1199}},
		Formats:     []string{"application/pdf"},
		Trays:       []string{"auto", "tray-1", "manual"},
		Qualities:   []Quality{QualityDraft, QualityNormal, QualityHigh},
	}
	if !reflect.DeepEqual(o.Caps, want) {
		t.Errorf("caps\n got %+v\nwant %+v", o.Caps, want)
	}

	caps, err := b.capabilities(context.Background(), "") // default printer
	if err != nil || !caps.Duplex {
		t.Errorf("capabilities(default) = %+v, %v", caps, err)
	}
	if _, err := b.capabilities(context.Background(), "Nope"); !errors.Is(err, ErrPrinterNotFound) {
		t.Errorf("capabilities(Nope) err = %v, want ErrPrinterNotFound", err)
	}
}

func TestIPPJobAttributes(t *testing.T) {
	yes := true
	s := Settings{
		Copies:      2,
		Collate:     &yes,
		PageRanges:  []PageRange{{1, 3}, {5, 0}},
		Media:       MediaA4,
		Orientation: Landscape,
		Duplex:      DuplexLongEdge,
		Color:       Monochrome,
		Quality:     QualityHigh,
		Scaling:     ScalingFit,
		Vendor:      map[string]string{"finishings": "4", "output-bin": "top", "x-flag": "true", "windows:output-file": "x"},
	}
	a, w := ippJobAttributes(s)
	get := func(name string) []ipp.Value {
		v, ok := a.Get(name)
		if !ok {
			t.Errorf("missing %s", name)
		}
		return v.Values
	}
	check := func(name string, want ...ipp.Value) {
		if got := get(name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("copies", ipp.Integer(2))
	check("multiple-document-handling", ipp.Keyword("separate-documents-collated-copies"))
	check("page-ranges", ipp.Range{Lower: 1, Upper: 3}, ipp.Range{Lower: 5, Upper: math.MaxInt32})
	check("media", ipp.Keyword("iso_a4_210x297mm"))
	check("orientation-requested", ipp.Enum(4))
	check("sides", ipp.Keyword("two-sided-long-edge"))
	check("print-color-mode", ipp.Keyword("monochrome"))
	check("print-quality", ipp.Enum(5))
	check("print-scaling", ipp.Keyword("fit"))
	check("finishings", ipp.Integer(4))
	check("output-bin", ipp.Keyword("top"))
	check("x-flag", ipp.Boolean(true))
	if len(w) != 1 || w[0].Setting != "Vendor[windows:output-file]" {
		t.Errorf("warnings = %v", w)
	}

	// Tray and custom sizes go through media-col.
	a, _ = ippJobAttributes(Settings{Media: MediaA5, Tray: "tray-2"})
	col, _ := a.Get("media-col")
	want := ipp.Collection{
		{Name: "media-size-name", Values: []ipp.Value{ipp.Keyword("iso_a5_148x210mm")}},
		{Name: "media-source", Values: []ipp.Value{ipp.Keyword("tray-2")}},
	}
	if !reflect.DeepEqual(col.Values, []ipp.Value{want}) {
		t.Errorf("media-col = %v", col.Values)
	}
	a, _ = ippJobAttributes(Settings{Media: Media{Width: 100000, Height: 150000}})
	col, _ = a.Get("media-col")
	size := col.Values[0].(ipp.Collection)[0].Values[0].(ipp.Collection)
	if x, _ := size[0].Int(); x != 10000 {
		t.Errorf("custom x-dimension = %v", size)
	}
	if _, ok := a.Get("media"); ok {
		t.Error("media keyword with media-col")
	}

	// Defaults send nothing.
	if a, w := ippJobAttributes(Settings{}); len(a) != 0 || len(w) != 0 {
		t.Errorf("zero settings → %v, %v", a, w)
	}
}

func TestIPPPrintAndTrack(t *testing.T) {
	b, srv := mockBackend(t, ipptest.Printer{Name: "Office", Default: true, Attrs: officeAttrs})
	ctx := context.Background()
	doc := pdfDoc("report")
	src, _ := doc.open()
	job, err := b.print(ctx, src, doc, Settings{Copies: 2, Duplex: DuplexLongEdge})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Warnings()) != 0 {
		t.Errorf("warnings %v", job.Warnings())
	}
	jobs := srv.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("server has %d jobs", len(jobs))
	}
	rec := jobs[0]
	if job.ID() != "1" || rec.Printer != "Office" || rec.User != "tester" || !bytes.HasPrefix(rec.Document, []byte("%PDF-")) {
		t.Errorf("job %s, recorded %+v", job.ID(), rec)
	}
	if n, _ := rec.Operation.Get("job-name"); n.String() != "report" {
		t.Errorf("job-name = %v", n)
	}
	if c, _ := rec.Attrs.Get("copies"); c.String() != "2" {
		t.Errorf("copies = %v", c)
	}

	if st, err := job.State(ctx); err != nil || st != JobPending {
		t.Errorf("State = %v, %v", st, err)
	}
	srv.SetJobState(1, ipp.JobProcessing)
	if st, _ := job.State(ctx); st != JobProcessing {
		t.Errorf("State = %v, want processing", st)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		srv.SetJobState(1, ipp.JobCompleted)
	}()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := job.Wait(wctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Cancel a second job.
	src, _ = doc.open()
	job2, err := b.print(ctx, src, doc, Settings{Printer: "Office"})
	if err != nil {
		t.Fatal(err)
	}
	if err := job2.Cancel(ctx); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := job2.Wait(wctx); !errors.Is(err, ErrCanceled) {
		t.Errorf("Wait after cancel = %v", err)
	}
}

func TestIPPPrintWarningsAndStrict(t *testing.T) {
	b, srv := mockBackend(t, ipptest.Printer{Name: "Office", Default: true, Attrs: officeAttrs})
	ctx := context.Background()
	doc := pdfDoc("x")

	// two-sided-short-edge is not in sides-supported: warning.
	src, _ := doc.open()
	job, err := b.print(ctx, src, doc, Settings{Duplex: DuplexShortEdge})
	if err != nil {
		t.Fatal(err)
	}
	if w := job.Warnings(); len(w) != 1 || w[0].Setting != "Duplex" {
		t.Errorf("warnings = %v, want one for Duplex", w)
	}

	// Strict: error, and the job is canceled.
	src, _ = doc.open()
	_, err = b.print(ctx, src, doc, Settings{Duplex: DuplexShortEdge, Strict: true})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict err = %v", err)
	}
	jobs := srv.Jobs()
	last := jobs[len(jobs)-1]
	if last.State != ipp.JobCanceled {
		t.Errorf("strict job state = %v, want canceled", last.State)
	}
	if f, ok := last.Operation.Get("ipp-attribute-fidelity"); !ok || f.String() != "true" {
		t.Errorf("ipp-attribute-fidelity = %v", f)
	}
}

func TestIPPErrors(t *testing.T) {
	ctx := context.Background()
	doc := pdfDoc("x")

	b, _ := mockBackend(t, ipptest.Printer{Name: "Office"}) // no default
	src, _ := doc.open()
	if _, err := b.print(ctx, src, doc, Settings{}); !errors.Is(err, ErrNoPrinter) {
		t.Errorf("no default: %v", err)
	}
	src, _ = doc.open()
	if _, err := b.print(ctx, src, doc, Settings{Printer: "Nope"}); !errors.Is(err, ErrPrinterNotFound) {
		t.Errorf("unknown printer: %v", err)
	}

	b, srv := mockBackend(t, ipptest.Printer{Name: "Office", Default: true})
	srv.InjectStatus(ipp.OpPrintJob, ipp.StatusErrorBusy, "busy")
	src, _ = doc.open()
	if _, err := b.print(ctx, src, doc, Settings{}); !errors.Is(err, ErrBusy) {
		t.Errorf("busy: %v", err)
	}

	b, srv = mockBackend(t, ipptest.Printer{Name: "Office", Default: true})
	srv.RequireAuth("alice", "secret")
	src, _ = doc.open()
	_, err := b.print(ctx, src, doc, Settings{})
	var se *ipp.StatusError
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorNotAuthenticated {
		t.Errorf("without credentials: %v", err)
	}
	src, _ = doc.open()
	if _, err := b.print(ctx, src, doc, Settings{Credentials: &Credentials{"alice", "secret"}}); err != nil {
		t.Errorf("with credentials: %v", err)
	}
}

// TestIPPPrinterURI prints to a printer addressed by ipp:// URI, which does
// not need the default (CUPS) server.
func TestIPPPrinterURI(t *testing.T) {
	srv := ipptest.NewServer(ipptest.Printer{Name: "Direct"})
	defer srv.Close()
	b := ippBackend{newClient: func(...ipp.Option) (*ipp.Client, error) {
		return nil, errors.New("default server must not be used")
	}}
	uri := "ipp://" + strings.TrimPrefix(srv.URL, "http://") + "/printers/Direct"
	doc := pdfDoc("direct")
	src, _ := doc.open()
	job, err := b.print(context.Background(), src, doc, Settings{Printer: uri})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID() != "1" || len(srv.Jobs()) != 1 || srv.Jobs()[0].Printer != "Direct" {
		t.Errorf("job %s, server jobs %+v", job.ID(), srv.Jobs())
	}
	if caps, err := b.capabilities(context.Background(), uri); err != nil {
		t.Errorf("capabilities(uri): %v (%+v)", err, caps)
	}
}
