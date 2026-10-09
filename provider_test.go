package goprint

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeProvider is an in-memory provider; it never reaches a printer.
type fakeProvider struct {
	name     string
	printers []Printer
	err      error

	printed []Settings
	data    []string
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Printers(context.Context) ([]Printer, error) {
	return p.printers, p.err
}

func (p *fakeProvider) Capabilities(_ context.Context, printer string) (Capabilities, error) {
	for _, pr := range p.printers {
		if pr.Name == printer {
			return pr.Caps, nil
		}
	}
	return Capabilities{}, ErrPrinterNotFound
}

func (p *fakeProvider) Print(_ context.Context, doc Document, s Settings) (*Job, error) {
	src, err := doc.open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	b, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	p.printed = append(p.printed, s)
	p.data = append(p.data, string(b))
	return NewJob(&fakeJob{id: "1"}, []Warning{{Setting: "Duplex", Message: "ignored"}}), nil
}

// dialogProvider adds a dialog that picks the first printer.
type dialogProvider struct{ fakeProvider }

func (p *dialogProvider) Dialog(_ context.Context, _ Document, opts DialogOptions) (*Job, Settings, error) {
	s := opts.Settings
	s.Printer = p.printers[0].Name
	return nil, s, nil
}

type fakeJob struct {
	id       string
	canceled bool
}

func (j *fakeJob) ID() string                              { return j.id }
func (j *fakeJob) State(context.Context) (JobState, error) { return JobCompleted, nil }
func (j *fakeJob) Wait(context.Context) error              { return nil }
func (j *fakeJob) Cancel(context.Context) error            { j.canceled = true; return nil }

func TestDefaultClientIsSystem(t *testing.T) {
	ps := Default.Providers()
	if len(ps) != 1 || ps[0].Name() != "" {
		t.Fatalf("Default providers = %v, want [System]", ps)
	}
	if _, ok := ps[0].(DialogProvider); !ok {
		t.Error("System is no DialogProvider")
	}
	if _, ok := ps[0].(PropertiesProvider); !ok {
		t.Error("System is no PropertiesProvider")
	}
}

func TestClientPrinters(t *testing.T) {
	a := &fakeProvider{name: "a", printers: []Printer{{Name: "P1", Default: true}}}
	b := &fakeProvider{name: "b", printers: []Printer{{Name: "P1", Provider: "wrong"}, {Name: "P2"}}}
	got, err := NewClient(a, b).Printers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Printer{{Provider: "a", Name: "P1", Default: true}, {Provider: "b", Name: "P1"}, {Provider: "b", Name: "P2"}}
	if len(got) != len(want) {
		t.Fatalf("Printers = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Provider != want[i].Provider || got[i].Name != want[i].Name || got[i].Default != want[i].Default {
			t.Errorf("printer %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestClientPrintersPartialFailure(t *testing.T) {
	a := &fakeProvider{name: "a", err: ErrBusy}
	b := &fakeProvider{name: "b", printers: []Printer{{Name: "P"}}}
	got, err := NewClient(a, b).Printers(context.Background())
	if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), `provider "a"`) {
		t.Errorf("err = %v, want ErrBusy naming provider a", err)
	}
	if len(got) != 1 || got[0].Name != "P" || got[0].Provider != "b" {
		t.Errorf("Printers = %+v, want b's printer", got)
	}
}

func TestClientPrintRoutes(t *testing.T) {
	a := &fakeProvider{name: "a"}
	b := &fakeProvider{name: "b"}
	c := NewClient(a, b)
	job, err := c.Print(context.Background(), PDFBytes("t", []byte("%PDF-b")), Settings{Provider: "b", Printer: "P"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.printed) != 0 || len(b.printed) != 1 || b.printed[0].Printer != "P" || b.data[0] != "%PDF-b" {
		t.Fatalf("a printed %v, b printed %v %q", a.printed, b.printed, b.data)
	}
	if job.ID() != "1" || len(job.Warnings()) != 1 {
		t.Errorf("job id %q, warnings %v", job.ID(), job.Warnings())
	}
	if st, err := job.State(context.Background()); err != nil || st != JobCompleted {
		t.Errorf("State = %v, %v", st, err)
	}
}

func TestClientUnknownProvider(t *testing.T) {
	c := NewClient(&fakeProvider{name: "a"})
	_, err := c.Print(context.Background(), PDFBytes("t", []byte("x")), Settings{Provider: "nope"})
	if !errors.Is(err, ErrPrinterNotFound) {
		t.Errorf("Print = %v, want ErrPrinterNotFound", err)
	}
	// "" is the system provider, which this client does not have.
	if _, err := c.Capabilities(context.Background(), "", ""); !errors.Is(err, ErrPrinterNotFound) {
		t.Errorf("Capabilities = %v, want ErrPrinterNotFound", err)
	}
}

func TestClientValidatesBeforeProvider(t *testing.T) {
	a := &fakeProvider{name: "a"}
	c := NewClient(a)
	if _, err := c.Print(context.Background(), Document{}, Settings{Provider: "a"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty document: %v, want ErrInvalid", err)
	}
	if _, err := c.Print(context.Background(), PDFBytes("t", []byte("x")), Settings{Provider: "a", Copies: -1}); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative copies: %v, want ErrInvalid", err)
	}
	if len(a.printed) != 0 {
		t.Errorf("provider called with invalid input: %v", a.printed)
	}
}

func TestClientDialog(t *testing.T) {
	d := &dialogProvider{fakeProvider{name: "d", printers: []Printer{{Name: "Label"}}}}
	c := NewClient(&fakeProvider{name: "a"}, d)
	_, s, err := c.Dialog(context.Background(), PDFBytes("t", []byte("x")), DialogOptions{Settings: Settings{Provider: "d"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Provider != "d" || s.Printer != "Label" {
		t.Errorf("chosen = %+v, want d/Label", s)
	}
	_, _, err = c.Dialog(context.Background(), PDFBytes("t", []byte("x")), DialogOptions{Settings: Settings{Provider: "a"}})
	if !errors.Is(err, ErrNoDialog) {
		t.Errorf("provider without dialog: %v, want ErrNoDialog", err)
	}
}

func TestClientPropertiesUnsupported(t *testing.T) {
	c := NewClient(&fakeProvider{name: "a"})
	if _, err := c.PrinterProperties(context.Background(), Settings{Provider: "a"}, 0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("PrinterProperties = %v, want ErrUnsupported", err)
	}
}

func TestNewClientDuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic for duplicate provider names")
		}
	}()
	NewClient(&fakeProvider{name: "a"}, &fakeProvider{name: "a"})
}

func TestNewJob(t *testing.T) {
	h := &fakeJob{id: "42"}
	w := []Warning{{Setting: "Color", Message: "x"}}
	j := NewJob(h, w)
	w[0].Message = "changed"
	if j.ID() != "42" || j.Warnings()[0].Message != "x" {
		t.Errorf("job = %q %v", j.ID(), j.Warnings())
	}
	if err := j.Wait(context.Background()); err != nil {
		t.Error(err)
	}
	if err := j.Cancel(context.Background()); err != nil || !h.canceled {
		t.Errorf("Cancel = %v, canceled %v", err, h.canceled)
	}
}
