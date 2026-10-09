package goprint

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

func TestDocumentAttributeKeys(t *testing.T) {
	for _, attrs := range []map[string]string{{"": "x"}, {"a=b": "x"}} {
		doc := pdfDoc("x")
		doc.Attributes = attrs
		if err := doc.validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v, want ErrInvalid", attrs, err)
		}
	}
	doc := pdfDoc("x")
	doc.Attributes = map[string]string{"customer": "4711", "note": "a=b"}
	if err := doc.validate(); err != nil {
		t.Errorf("valid attributes: %v", err)
	}
	if got := ippAttributeValues(doc.Attributes); !reflect.DeepEqual(got, []ipp.Value{ipp.Text("customer=4711"), ipp.Text("note=a=b")}) {
		t.Errorf("values %v", got)
	}
}

var takesGoprintAttributes = ipp.Attributes{{Name: "job-creation-attributes-supported",
	Values: []ipp.Value{ipp.Keyword("copies"), ipp.Keyword(IPPAttributes)}}}

func TestIPPSendsAttributesOnlyWhereTaken(t *testing.T) {
	b, srv := mockBackend(t, ipptest.Printer{Name: "DMS", Attrs: takesGoprintAttributes}, ipptest.Printer{Name: "Plain"})
	doc := pdfDoc("Invoice")
	doc.Attributes = map[string]string{"customer": "4711", "type": "invoice"}
	print := func(printer string, strict bool) (*Job, error) {
		src, _ := doc.open()
		defer src.Close()
		return b.print(context.Background(), src, doc, Settings{Printer: printer, Strict: strict})
	}

	job, err := print("DMS", false)
	if err != nil || len(job.Warnings()) != 0 {
		t.Fatalf("DMS: %v, warnings %v", err, job.Warnings())
	}
	if a, ok := srv.Jobs()[0].Attrs.Get(IPPAttributes); !ok || !reflect.DeepEqual(a.Strings(), []string{"customer=4711", "type=invoice"}) {
		t.Errorf("sent %v", a)
	}

	job, err = print("Plain", false)
	if err != nil {
		t.Fatal(err)
	}
	if w := job.Warnings(); len(w) != 1 || w[0].Setting != "Attributes" {
		t.Errorf("warnings %v", w)
	}
	if _, ok := srv.Jobs()[1].Attrs.Get(IPPAttributes); ok {
		t.Error("attributes sent to a printer that does not take them")
	}

	if _, err := print("Plain", true); !errors.Is(err, ErrUnsupported) {
		t.Errorf("strict: %v", err)
	}
	if n := len(srv.Jobs()); n != 2 {
		t.Errorf("%d jobs", n)
	}
}

// dialogBackend is a backend whose dialog always prints.
type dialogBackend struct{ backend }

func (dialogBackend) dialog(context.Context, Document, DialogOptions) (*Job, Settings, error) {
	return &Job{b: handleJob{&fakeJob{id: "1"}}}, Settings{}, nil
}

func TestDialogAttributesWarning(t *testing.T) {
	p := systemProvider{dialogBackend{}}
	doc := pdfDoc("x")
	doc.Attributes = map[string]string{"k": "v"}
	job, _, err := p.Dialog(context.Background(), doc, DialogOptions{PrintNow: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := job.Warnings(); len(w) != 1 || w[0].Setting != "Attributes" {
		t.Errorf("warnings %v", w)
	}
	if _, _, err := p.Dialog(context.Background(), doc, DialogOptions{Settings: Settings{Strict: true}}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("strict: %v", err)
	}
}
