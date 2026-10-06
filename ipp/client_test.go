package ipp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

func testPrinters() []ipptest.Printer {
	return []ipptest.Printer{
		{Name: "Office", Default: true, Attrs: ipp.Attributes{
			{Name: "printer-info", Values: []ipp.Value{ipp.Text("Office printer")}},
			{Name: "printer-state", Values: []ipp.Value{ipp.Enum(ipp.PrinterProcessing)}},
			{Name: "printer-state-reasons", Values: []ipp.Value{ipp.Keyword("media-low-warning"), ipp.Keyword("toner-low-report")}},
			{Name: "copies-supported", Values: []ipp.Value{ipp.Range{Lower: 1, Upper: 99}}},
			{Name: "sides-supported", Values: []ipp.Value{ipp.Keyword("one-sided"), ipp.Keyword("two-sided-long-edge")}},
			{Name: "print-color-mode-supported", Values: []ipp.Value{ipp.Keyword("monochrome")}},
			{Name: "media-col-default", Values: []ipp.Value{ipp.Collection{
				{Name: "media-size", Values: []ipp.Value{ipp.Collection{
					{Name: "x-dimension", Values: []ipp.Value{ipp.Integer(21000)}},
					{Name: "y-dimension", Values: []ipp.Value{ipp.Integer(29700)}},
				}}},
			}}},
		}},
		{Name: "Label Writer"},
	}
}

func newTest(t *testing.T, opts ...ipp.Option) (*ipptest.Server, *ipp.Client) {
	t.Helper()
	srv := ipptest.NewServer(testPrinters()...)
	t.Cleanup(srv.Close)
	c, err := ipp.NewClient(srv.URL, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

func TestCUPSGetPrinters(t *testing.T) {
	srv, c := newTest(t)
	ps, err := c.CUPSGetPrinters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d printers, want 2", len(ps))
	}
	p := ps[0]
	host := strings.TrimPrefix(srv.URL, "http://")
	if p.Name != "Office" || p.URI != "ipp://"+host+"/printers/Office" || p.State != ipp.PrinterProcessing ||
		!reflect.DeepEqual(p.StateReasons, []string{"media-low-warning", "toner-low-report"}) {
		t.Errorf("printer = %+v", p)
	}
	if a, ok := p.Attrs.Get("media-col-default"); !ok || a.String() != "{media-size={x-dimension=21000 y-dimension=29700}}" {
		t.Errorf("media-col-default = %v", a)
	}
	if ps[1].Name != "Label Writer" || ps[1].State != ipp.PrinterIdle || ps[1].URI != "ipp://"+host+"/printers/Label%20Writer" {
		t.Errorf("printer 2 = %+v", ps[1])
	}

	// requested-attributes filters the response.
	ps, err = c.CUPSGetPrinters(context.Background(), "printer-name")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps[0].Attrs) != 1 || ps[0].Name != "Office" {
		t.Errorf("filtered attrs = %+v", ps[0].Attrs)
	}

	// The mandatory operation attributes are present in order.
	req := srv.Requests()[0]
	if req.Operation() != ipp.OpCUPSGetPrinters || req.Version != ipp.Version20 || req.RequestID <= 0 {
		t.Errorf("request header = %v %v %d", req.Operation(), req.Version, req.RequestID)
	}
	var names []string
	for _, a := range req.Groups[0].Attrs {
		names = append(names, a.Name)
	}
	want := []string{"attributes-charset", "attributes-natural-language", "requesting-user-name"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("operation attributes = %v, want %v", names, want)
	}
	if u, _ := req.Groups[0].Attrs.Get("requesting-user-name"); u.String() != c.UserName() || c.UserName() == "" {
		t.Errorf("requesting-user-name = %q, client %q", u.String(), c.UserName())
	}
	if ids := srv.Requests(); ids[1].RequestID == ids[0].RequestID {
		t.Error("request ids not unique")
	}
}

func TestCUPSGetPrintersEmpty(t *testing.T) {
	srv := ipptest.NewServer()
	defer srv.Close()
	c, err := ipp.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := c.CUPSGetPrinters(context.Background())
	if err != nil || len(ps) != 0 {
		t.Errorf("CUPSGetPrinters = %v, %v; want empty, nil", ps, err)
	}
	_, err = c.CUPSGetDefault(context.Background())
	var se *ipp.StatusError
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorNotFound || se.Message != "No default printer." {
		t.Errorf("CUPSGetDefault err = %v, want not-found", err)
	}
}

func TestCUPSGetDefault(t *testing.T) {
	_, c := newTest(t)
	p, err := c.CUPSGetDefault(context.Background(), "printer-name", "printer-info")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Office" || len(p.Attrs) != 2 {
		t.Errorf("default = %+v", p)
	}
}

func TestGetPrinterAttributes(t *testing.T) {
	srv, c := newTest(t)
	for _, printer := range []string{"Label Writer", c.PrinterURI("Label Writer")} {
		p, err := c.GetPrinterAttributes(context.Background(), printer, "all")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "Label Writer" {
			t.Errorf("printer = %+v", p)
		}
	}
	reqs := srv.Requests()
	if a, _ := reqs[0].Groups[0].Attrs.Get("printer-uri"); a.String() != c.PrinterURI("Label Writer") {
		t.Errorf("printer-uri = %q", a.String())
	}
	_, err := c.GetPrinterAttributes(context.Background(), "Nope")
	var se *ipp.StatusError
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorNotFound {
		t.Errorf("err = %v, want not-found", err)
	}
}

func TestPrintJobLifecycle(t *testing.T) {
	srv, c := newTest(t, ipp.WithUserName("alice"))
	ctx := context.Background()
	doc := []byte("%PDF-1.7\n...binary\x00\xff...")
	job, err := c.PrintJob(ctx, "Office", bytes.NewReader(doc), &ipp.PrintJobOptions{
		JobName: "Invoice 4711",
		Job: ipp.Attributes{
			{Name: "copies", Values: []ipp.Value{ipp.Integer(2)}},
			{Name: "sides", Values: []ipp.Value{ipp.Keyword("two-sided-long-edge")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != 1 || job.State != ipp.JobPending || job.Status != ipp.StatusOK || len(job.Unsupported) != 0 ||
		!strings.HasSuffix(job.URI, "/jobs/1") || !reflect.DeepEqual(job.StateReasons, []string{"none"}) {
		t.Errorf("job = %+v", job)
	}
	jobs := srv.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("server has %d jobs", len(jobs))
	}
	got := jobs[0]
	if !bytes.Equal(got.Document, doc) || got.User != "alice" || got.Printer != "Office" {
		t.Errorf("recorded job = %+v", got)
	}
	if a, _ := got.Operation.Get("document-format"); a.String() != ipp.DefaultDocumentFormat {
		t.Errorf("document-format = %q", a.String())
	}
	if a, _ := got.Operation.Get("job-name"); a.String() != "Invoice 4711" {
		t.Errorf("job-name = %q", a.String())
	}
	if a, _ := got.Attrs.Get("copies"); a.String() != "2" {
		t.Errorf("copies = %q", a.String())
	}
	if _, ok := got.Operation.Get("printer-uri"); !ok {
		t.Error("missing printer-uri")
	}

	srv.SetJobState(job.ID, ipp.JobProcessing)
	j, err := c.GetJobAttributes(ctx, "Office", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.ID != 1 || j.State != ipp.JobProcessing {
		t.Errorf("Get-Job-Attributes = %+v", j)
	}
	j, err = c.GetJobAttributes(ctx, "Office", job.ID, "job-state")
	if err != nil || len(j.Attrs) != 1 || j.State != ipp.JobProcessing {
		t.Errorf("filtered Get-Job-Attributes = %+v, %v", j, err)
	}

	if err := c.CancelJob(ctx, "Office", job.ID); err != nil {
		t.Fatal(err)
	}
	if srv.Jobs()[0].State != ipp.JobCanceled {
		t.Errorf("state after cancel = %v", srv.Jobs()[0].State)
	}
	j, err = c.GetJobAttributes(ctx, "Office", job.ID)
	if err != nil || j.State != ipp.JobCanceled || j.StateReasons[0] != "job-canceled-by-user" {
		t.Errorf("after cancel: %+v, %v", j, err)
	}
	err = c.CancelJob(ctx, "Office", job.ID)
	var se *ipp.StatusError
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorNotPossible {
		t.Errorf("second cancel err = %v, want not-possible", err)
	}
	if err := c.CancelJob(ctx, "Label Writer", job.ID); !errors.As(err, &se) || se.Code != ipp.StatusErrorNotFound {
		t.Errorf("cancel on other printer err = %v, want not-found", err)
	}
	if srv.SetJobState(99, ipp.JobAborted) {
		t.Error("SetJobState(99) = true")
	}
}

func TestPrintJobUnsupported(t *testing.T) {
	_, c := newTest(t)
	job, err := c.PrintJob(context.Background(), "Office", strings.NewReader("x"), &ipp.PrintJobOptions{
		DocumentFormat: "image/pwg-raster",
		Job: ipp.Attributes{
			{Name: "copies", Values: []ipp.Value{ipp.Integer(100)}},
			{Name: "print-color-mode", Values: []ipp.Value{ipp.Keyword("color")}},
			{Name: "sides", Values: []ipp.Value{ipp.Keyword("one-sided")}},
			{Name: "media-col", Values: []ipp.Value{ipp.Collection{{Name: "media-source", Values: []ipp.Value{ipp.Keyword("tray-1")}}}}},
		},
	})
	if err != nil {
		t.Fatalf("PrintJob: %v", err)
	}
	if job.Status != ipp.StatusOKIgnoredOrSubstituted {
		t.Errorf("status = %v", job.Status)
	}
	var names []string
	for _, a := range job.Unsupported {
		names = append(names, a.Name)
	}
	if !reflect.DeepEqual(names, []string{"copies", "print-color-mode"}) {
		t.Errorf("unsupported = %v", names)
	}
}

func TestPrintJobInvalid(t *testing.T) {
	_, c := newTest(t)
	ctx := context.Background()
	if _, err := c.PrintJob(ctx, "Office", nil, nil); !errors.Is(err, ipp.ErrInvalid) {
		t.Errorf("nil doc err = %v", err)
	}
	if _, err := c.GetJobAttributes(ctx, "Office", 0); !errors.Is(err, ipp.ErrInvalid) {
		t.Errorf("job 0 err = %v", err)
	}
	if err := c.CancelJob(ctx, "Office", -1); !errors.Is(err, ipp.ErrInvalid) {
		t.Errorf("negative job err = %v", err)
	}
	// nil options use the defaults.
	if _, err := c.PrintJob(ctx, "Office", strings.NewReader("x"), nil); err != nil {
		t.Errorf("PrintJob(nil opts): %v", err)
	}
}

func TestInjectStatus(t *testing.T) {
	srv, c := newTest(t)
	ctx := context.Background()
	srv.InjectStatus(ipp.OpPrintJob, ipp.StatusErrorNotAcceptingJobs, "Printer is paused.")
	_, err := c.PrintJob(ctx, "Office", strings.NewReader("x"), nil)
	var se *ipp.StatusError
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorNotAcceptingJobs || se.Message != "Printer is paused." {
		t.Fatalf("err = %v", err)
	}
	if got := se.Error(); got != "ipp: server-error-not-accepting-jobs: Printer is paused." {
		t.Errorf("Error() = %q", got)
	}
	// Do returns the response alongside the error.
	resp, err := c.Do(ctx, ipp.NewRequest(ipp.OpPrintJob, 0), nil)
	if resp == nil || err == nil || resp.Status() != ipp.StatusErrorNotAcceptingJobs {
		t.Errorf("Do = %v, %v", resp, err)
	}
	srv.InjectStatus(ipp.OpPrintJob, ipp.StatusOK, "")
	if _, err := c.PrintJob(ctx, "Office", strings.NewReader("x"), nil); err != nil {
		t.Errorf("after clearing: %v", err)
	}
	srv.InjectStatus(ipp.OpCUPSGetPrinters, ipp.StatusErrorInternal, "")
	if _, err := c.CUPSGetPrinters(ctx); !errors.As(err, &se) || se.Error() != "ipp: server-error-internal-error" {
		t.Errorf("CUPSGetPrinters err = %v", err)
	}
	srv.InjectStatus(ipp.OpGetPrinterAttributes, ipp.StatusErrorBusy, "")
	if _, err := c.GetPrinterAttributes(ctx, "Office"); !errors.As(err, &se) || se.Code != ipp.StatusErrorBusy {
		t.Errorf("GetPrinterAttributes err = %v", err)
	}
	srv.InjectStatus(ipp.OpGetJobAttributes, ipp.StatusErrorGone, "")
	if _, err := c.GetJobAttributes(ctx, "Office", 1); !errors.As(err, &se) || se.Code != ipp.StatusErrorGone {
		t.Errorf("GetJobAttributes err = %v", err)
	}
	srv.InjectStatus(ipp.OpCUPSGetDefault, ipp.StatusErrorForbidden, "")
	if _, err := c.CUPSGetDefault(ctx); !errors.As(err, &se) || se.Code != ipp.StatusErrorForbidden {
		t.Errorf("CUPSGetDefault err = %v", err)
	}
}

func TestLatencyAndCancel(t *testing.T) {
	srv, c := newTest(t)
	srv.SetLatency(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.CUPSGetPrinters(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("returned after %v", d)
	}
	srv.SetLatency(10 * time.Millisecond)
	if _, err := c.CUPSGetPrinters(context.Background()); err != nil {
		t.Errorf("with small latency: %v", err)
	}
}

func TestBasicAuth(t *testing.T) {
	srv := ipptest.NewServer(testPrinters()...)
	defer srv.Close()
	srv.RequireAuth("bob", "secret")
	ctx := context.Background()

	c, _ := ipp.NewClient(srv.URL)
	_, err := c.CUPSGetPrinters(ctx)
	var se *ipp.StatusError
	if !errors.As(err, &se) || se.Code != ipp.StatusErrorNotAuthenticated {
		t.Errorf("without credentials err = %v", err)
	}
	c, _ = ipp.NewClient(srv.URL, ipp.WithCredentials("bob", "wrong"))
	if _, err := c.CUPSGetPrinters(ctx); !errors.As(err, &se) {
		t.Errorf("wrong password err = %v", err)
	}
	c, _ = ipp.NewClient(srv.URL, ipp.WithCredentials("bob", "secret"))
	if _, err := c.CUPSGetPrinters(ctx); err != nil {
		t.Errorf("with credentials: %v", err)
	}
	if c.UserName() != "bob" {
		t.Errorf("UserName = %q, want bob", c.UserName())
	}
	c, _ = ipp.NewClient(srv.URL, ipp.WithCredentials("bob", "secret"), ipp.WithUserName("carol"))
	if c.UserName() != "carol" {
		t.Errorf("UserName = %q, want carol", c.UserName())
	}
}

func TestUnixSocket(t *testing.T) {
	srv := ipptest.NewServer(testPrinters()...)
	defer srv.Close()
	if srv.SocketPath == "" {
		t.Skip("unix sockets not supported")
	}
	for _, addr := range []string{srv.SocketPath, "unix://" + srv.SocketPath} {
		c, err := ipp.NewClient(addr, ipp.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		ps, err := c.CUPSGetPrinters(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		if len(ps) != 2 || ps[0].URI != "ipp://localhost/printers/Office" {
			t.Errorf("%s: printers = %+v", addr, ps)
		}
		job, err := c.PrintJob(context.Background(), "Office", strings.NewReader("doc"), nil)
		if err != nil || job.ID == 0 {
			t.Errorf("%s: PrintJob = %+v, %v", addr, job, err)
		}
	}
	// A custom transport is cloned and keeps working.
	c, err := ipp.NewClient(srv.SocketPath, ipp.WithHTTPClient(&http.Client{Transport: &http.Transport{}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CUPSGetDefault(context.Background()); err != nil {
		t.Errorf("custom transport: %v", err)
	}
}

func TestIPPScheme(t *testing.T) {
	srv := ipptest.NewServer(testPrinters()...)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	c, err := ipp.NewClient("ipp://" + host + "/ignored?q=1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.PrinterURI("A B"), "ipp://"+host+"/printers/A%20B"; got != want {
		t.Errorf("PrinterURI = %q, want %q", got, want)
	}
	p, err := c.GetPrinterAttributes(context.Background(), "ipp://"+host+"/printers/Office")
	if err != nil || p.Name != "Office" {
		t.Errorf("GetPrinterAttributes = %+v, %v", p, err)
	}
}

func TestEndpointPath(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)
	srv := ipptest.NewServer(testPrinters()...)
	defer srv.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		srv.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	c, err := ipp.NewClient(proxy.URL + "/base")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = c.CUPSGetPrinters(ctx)
	_, _ = c.GetPrinterAttributes(ctx, "Office")
	req := ipp.NewRequest(ipp.OpGetJobAttributes, 0)
	req.Groups[0].Attrs.Add("job-uri", ipp.URI("ipp://elsewhere/jobs/3"))
	_, _ = c.Do(ctx, req, nil)
	req = ipp.NewRequest(ipp.OpGetJobAttributes, 0)
	req.Groups = nil
	req.Version = 0
	_, _ = c.Do(ctx, req, nil)
	want := []string{"/base", "/printers/Office", "/jobs/3", "/base"}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestHTTPErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"status 500", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}},
		{"garbage body", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", ipp.ContentType)
			_, _ = w.Write([]byte{2, 0, 0})
		}},
		{"no printer group", func(w http.ResponseWriter, _ *http.Request) {
			_ = ipp.NewResponse(ipp.StatusOK, 1).Encode(w)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(tt.handler)
			defer ts.Close()
			c, err := ipp.NewClient(ts.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.GetPrinterAttributes(context.Background(), "x"); err == nil {
				t.Error("no error")
			}
		})
	}
	// Connection refused.
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()
	c, _ := ipp.NewClient(url)
	if _, err := c.CUPSGetPrinters(context.Background()); err == nil {
		t.Error("no error for closed server")
	}
	// Unencodable request.
	req := ipp.NewRequest(ipp.OpPrintJob, 0)
	req.Groups[0].Attrs.Add("bad")
	if _, err := c.Do(context.Background(), req, nil); !errors.Is(err, ipp.ErrInvalid) {
		t.Errorf("unencodable request err = %v", err)
	}
}

func TestServerRejects(t *testing.T) {
	srv, c := newTest(t)
	ctx := context.Background()
	post := func(ct string, body []byte) int {
		resp, err := http.Post(srv.URL, ct, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if code := post("text/plain", nil); code != http.StatusUnsupportedMediaType {
		t.Errorf("wrong content type: HTTP %d", code)
	}
	if code := post(ipp.ContentType, []byte{1, 2}); code != http.StatusBadRequest {
		t.Errorf("garbage: HTTP %d", code)
	}
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: HTTP %d", resp.StatusCode)
	}

	var se *ipp.StatusError
	check := func(name string, req *ipp.Message, want ipp.Status) {
		t.Helper()
		_, err := c.Do(ctx, req, nil)
		if want == ipp.StatusOK {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			return
		}
		if !errors.As(err, &se) || se.Code != want {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
	}
	req := ipp.NewRequest(ipp.OpGetPrinterAttributes, 0)
	req.Version = 0x0300
	check("version", req, ipp.StatusErrorVersionNotSupported)
	req = ipp.NewRequest(ipp.OpGetPrinterAttributes, 0)
	req.Groups[0].Attrs = req.Groups[0].Attrs[1:]
	check("no charset", req, ipp.StatusErrorBadRequest)
	check("no printer-uri", ipp.NewRequest(ipp.OpGetPrinterAttributes, 0), ipp.StatusErrorBadRequest)
	req = ipp.NewRequest(ipp.OpGetPrinterAttributes, 0)
	req.Groups[0].Attrs.Add("printer-uri", ipp.URI("ipp://x/%zz"))
	check("bad printer-uri", req, ipp.StatusErrorBadRequest)
	req = ipp.NewRequest(ipp.OpGetPrinterAttributes, 0)
	req.Groups[0].Attrs.Add("printer-uri", ipp.URI("ipp://x/classes/Office"))
	check("not a printer path", req, ipp.StatusErrorNotFound)
	req = ipp.NewRequest(ipp.OpPausePrinter, 0)
	req.Groups[0].Attrs.Add("printer-uri", ipp.URI(c.PrinterURI("Office")))
	check("unsupported op", req, ipp.StatusErrorOperationNotSupported)
}

func TestServerAddPrinter(t *testing.T) {
	srv, c := newTest(t)
	srv.AddPrinter(ipptest.Printer{Name: "New"})
	srv.AddPrinter(ipptest.Printer{Name: "Office", Attrs: ipp.Attributes{
		{Name: "printer-state", Values: []ipp.Value{ipp.Enum(ipp.PrinterStopped)}},
	}})
	ps, err := c.CUPSGetPrinters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].State != ipp.PrinterStopped || ps[2].Name != "New" {
		t.Errorf("printers = %+v", ps)
	}
	// Office is no longer the default.
	if _, err := c.CUPSGetDefault(context.Background()); err == nil {
		t.Error("CUPSGetDefault: no error")
	}
}

type slowReader struct {
	ctx context.Context
}

func (r slowReader) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestPrintJobDocumentError(t *testing.T) {
	_, c := newTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.PrintJob(ctx, "Office", slowReader{ctx}, nil); err == nil {
		t.Error("no error for failing document reader")
	}
}
