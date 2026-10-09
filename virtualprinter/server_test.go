package virtualprinter

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/testpdf"
	"github.com/timzifer/goprint/ipp"
)

// serve starts a server for vp on a loopback port.
func serve(t *testing.T, vp *Provider) *Server {
	t.Helper()
	srv, err := vp.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

// network is goprint's network provider, which reaches the server by URI.
var network = goprint.NewClient(goprint.IPPEverywhere(goprint.IPPEverywhereOptions{}))

func a4PDF() []byte { return testpdf.Generate(2, testpdf.A4Width, testpdf.A4Height) }

func TestServerPrint(t *testing.T) {
	vp := New("v", Office("Office"))
	srv := serve(t, vp)
	pdf := a4PDF()
	collate := true
	s := goprint.Settings{
		Provider: "ipp", Printer: srv.PrinterURI("Office"),
		Copies: 2, Collate: &collate, PageRanges: []goprint.PageRange{{From: 2, To: 0}},
		Media: goprint.MediaA5, Orientation: goprint.Landscape, Duplex: goprint.DuplexLongEdge,
		Color: goprint.Monochrome, Quality: goprint.QualityHigh, Scaling: goprint.ScalingFit, Tray: "manual",
	}
	job, err := network.Print(context.Background(), goprint.PDFBytes("Invoice 42", pdf), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Warnings()) != 0 {
		t.Errorf("warnings %v", job.Warnings())
	}
	jobs := vp.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("%d jobs", len(jobs))
	}
	j := jobs[0]
	if j.Title != "Invoice 42" || j.Printer != "Office" || !bytes.Equal(j.PDF, pdf) {
		t.Errorf("job %q on %q, %d bytes", j.Title, j.Printer, len(j.PDF))
	}
	want := s
	want.Provider, want.Printer = "", "Office"
	if !reflect.DeepEqual(j.Settings, want) {
		t.Errorf("settings\n got %+v\nwant %+v", j.Settings, want)
	}
	if job.ID() != "1" {
		t.Errorf("job id %q", job.ID())
	}
	if err := job.Wait(context.Background()); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestServerCapabilities(t *testing.T) {
	vp := New("v", Office("Office"))
	srv := serve(t, vp)
	got, err := network.Capabilities(context.Background(), "ipp", srv.PrinterURI("Office"))
	if err != nil {
		t.Fatal(err)
	}
	want := Office("Office").Caps
	want.Formats = []string{"application/pdf", "application/octet-stream"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("caps\n got %+v\nwant %+v", got, want)
	}
}

func TestServerWarningsAndStrict(t *testing.T) {
	vp := New("v", Label("Label"))
	srv := serve(t, vp)
	s := goprint.Settings{Provider: "ipp", Printer: srv.PrinterURI("Label"), Duplex: goprint.DuplexLongEdge}
	job, err := network.Print(context.Background(), goprint.PDFBytes("x", a4PDF()), s)
	if err != nil {
		t.Fatal(err)
	}
	if w := job.Warnings(); len(w) != 1 || w[0].Setting != "Duplex" {
		t.Errorf("warnings %v", w)
	}
	s.Strict = true
	if _, err := network.Print(context.Background(), goprint.PDFBytes("x", a4PDF()), s); !errors.Is(err, goprint.ErrUnsupported) {
		t.Errorf("strict: %v", err)
	}
	if n := len(vp.Jobs()); n != 1 {
		t.Errorf("%d jobs", n)
	}
}

func TestServerScriptedFailures(t *testing.T) {
	vp := New("v", Office("Office"))
	srv := serve(t, vp)
	s := goprint.Settings{Provider: "ipp", Printer: srv.PrinterURI("Office")}
	doc := goprint.PDFBytes("x", a4PDF())

	vp.SetOffline("Office", true)
	if _, err := network.Print(context.Background(), doc, s); !errors.Is(err, goprint.ErrBusy) {
		t.Errorf("offline: %v", err)
	}
	vp.SetOffline("Office", false)

	vp.FailNext(errors.New("paper jam"))
	if _, err := network.Print(context.Background(), doc, s); err == nil || !strings.Contains(err.Error(), "paper jam") {
		t.Errorf("FailNext: %v", err)
	}

	// A held job is followed and canceled over IPP.
	vp.HoldJobs(true)
	job, err := network.Print(context.Background(), doc, s)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := job.State(context.Background()); err != nil || st != goprint.JobPending {
		t.Errorf("held state %v, %v", st, err)
	}
	if err := job.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := vp.Jobs()[0].State(); st != goprint.JobCanceled {
		t.Errorf("after cancel: %v", st)
	}
	if err := job.Wait(context.Background()); !errors.Is(err, goprint.ErrCanceled) {
		t.Errorf("Wait: %v", err)
	}

	bad := s
	bad.Printer = srv.PrinterURI("Gone")
	if _, err := network.Print(context.Background(), doc, bad); !errors.Is(err, goprint.ErrPrinterNotFound) {
		t.Errorf("unknown printer: %v", err)
	}
}

// request sends a raw IPP request to the printer.
func request(t *testing.T, c *ipp.Client, op ipp.Operation, uri string, attrs ipp.Attributes, doc []byte) (*ipp.Message, error) {
	t.Helper()
	req := ipp.NewRequest(op, 1)
	req.Groups[0].Attrs = append(req.Groups[0].Attrs,
		ipp.Attribute{Name: "printer-uri", Values: []ipp.Value{ipp.URI(uri)}},
		ipp.Attribute{Name: "requesting-user-name", Values: []ipp.Value{ipp.Name("tester")}})
	req.Groups[0].Attrs = append(req.Groups[0].Attrs, attrs...)
	var body *bytes.Reader
	if doc != nil {
		body = bytes.NewReader(doc)
		return c.Do(context.Background(), req, body)
	}
	return c.Do(context.Background(), req, nil)
}

func TestServerCreateJobSendDocument(t *testing.T) {
	vp := New("v", Office("Office"))
	srv := serve(t, vp)
	uri := srv.PrinterURI("Office")
	c, err := ipp.NewClient(uri)
	if err != nil {
		t.Fatal(err)
	}
	req := ipp.NewRequest(ipp.OpCreateJob, 1)
	req.Groups[0].Attrs = append(req.Groups[0].Attrs,
		ipp.Attribute{Name: "printer-uri", Values: []ipp.Value{ipp.URI(uri)}},
		ipp.Attribute{Name: "job-name", Values: []ipp.Value{ipp.Name("Two step")}})
	req.Groups = append(req.Groups, ipp.Group{Tag: ipp.TagJobGroup, Attrs: ipp.Attributes{
		{Name: "copies", Values: []ipp.Value{ipp.Integer(3)}},
	}})
	resp, err := c.Do(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := resp.Group(ipp.TagJobGroup).Attrs.Get("job-id")
	id, _ := a.Int()
	if len(vp.Jobs()) != 0 {
		t.Fatal("job printed before its document")
	}
	_, err = request(t, c, ipp.OpSendDocument, uri, ipp.Attributes{
		{Name: "job-id", Values: []ipp.Value{ipp.Integer(int32(id))}},
		{Name: "document-format", Values: []ipp.Value{ipp.MimeMediaType("application/pdf")}},
		{Name: "last-document", Values: []ipp.Value{ipp.Boolean(true)}},
	}, a4PDF())
	if err != nil {
		t.Fatal(err)
	}
	jobs := vp.Jobs()
	if len(jobs) != 1 || jobs[0].ID != id || jobs[0].Title != "Two step" || jobs[0].Settings.Copies != 3 {
		t.Fatalf("jobs %+v", jobs)
	}

	// Get-Jobs lists it.
	resp, err = request(t, c, ipp.OpGetJobs, uri, nil, nil)
	if err != nil || len(resp.GroupsByTag(ipp.TagJobGroup)) != 1 {
		t.Errorf("Get-Jobs: %v, %v", resp, err)
	}
}

func TestServerRejectsNonPDF(t *testing.T) {
	vp := New("v", Office("Office"))
	srv := serve(t, vp)
	uri := srv.PrinterURI("Office")
	c, _ := ipp.NewClient(uri)
	_, err := request(t, c, ipp.OpPrintJob, uri, ipp.Attributes{
		{Name: "document-format", Values: []ipp.Value{ipp.MimeMediaType("image/urf")}},
	}, []byte("UNIRAST\x00"))
	var se *ipp.StatusError
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorDocumentFormat {
		t.Errorf("URF: %v", err)
	}
	_, err = request(t, c, ipp.OpPrintJob, uri, nil, []byte("not a pdf"))
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorDocumentFormatError {
		t.Errorf("no PDF: %v", err)
	}
	if len(vp.Jobs()) != 0 {
		t.Error("job created")
	}
}

func TestSettingsFromIPP(t *testing.T) {
	s, bad := settingsFromIPP(ipp.Attributes{
		{Name: "media-col", Values: []ipp.Value{ipp.Collection{
			{Name: "media-size", Values: []ipp.Value{ipp.Collection{
				{Name: "x-dimension", Values: []ipp.Value{ipp.Integer(6200)}},
				{Name: "y-dimension", Values: []ipp.Value{ipp.Integer(10000)}},
			}}},
			{Name: "media-source", Values: []ipp.Value{ipp.Keyword("roll")}},
		}}},
		{Name: "sides", Values: []ipp.Value{ipp.Keyword("sideways")}},
		{Name: "acme-finish", Values: []ipp.Value{ipp.Keyword("staple")}},
	})
	if s.Media != (goprint.Media{Width: 62000, Height: 100000}) || s.Tray != "roll" || s.Vendor["acme-finish"] != "staple" {
		t.Errorf("settings %+v", s)
	}
	if len(bad) != 1 || bad[0].Name != "sides" {
		t.Errorf("unsupported %v", bad)
	}
}
