package goprint

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"testing"

	"github.com/timzifer/goprint/internal/dnssd"
	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

// networkMock serves printers through ipptest and announces them through
// a fake DNS-SD browse; nothing leaves the machine.
func networkMock(t *testing.T, printers ...ipptest.Printer) (*ippEverywhere, *ipptest.Server, *int) {
	t.Helper()
	srv := ipptest.NewServer(printers...)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	browses := 0
	p := IPPEverywhere(IPPEverywhereOptions{}).(*ippEverywhere)
	p.browse = func(context.Context, ...string) ([]dnssd.Service, error) {
		browses++
		var out []dnssd.Service
		for _, pr := range printers {
			out = append(out, dnssd.Service{
				Instance: pr.Name, Type: "_ipp._tcp", Host: "mock.local", Port: port,
				Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
				TXT:   map[string]string{"rp": "printers/" + pr.Name, "ty": "Mock " + pr.Name, "note": "Lab", "pdl": "application/pdf", "color": "T"},
			})
		}
		// The same printer over IPPS loses against plain IPP.
		out = append(out, dnssd.Service{Instance: "Office", Type: "_ipps._tcp", Host: "mock.local", Port: 443})
		return out, nil
	}
	return p, srv, &browses
}

var urfOnly = ipp.Attributes{{Name: "document-format-supported", Values: []ipp.Value{ipp.MimeMediaType("image/urf"), ipp.MimeMediaType("image/pwg-raster")}}}

func TestIPPEverywherePrinters(t *testing.T) {
	p, _, _ := networkMock(t, ipptest.Printer{Name: "Office", Attrs: officeAttrs}, ipptest.Printer{Name: "Lab"})
	got, err := NewClient(p).Printers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Lab" || got[1].Name != "Office" {
		t.Fatalf("Printers = %+v", got)
	}
	o := got[1]
	if o.Provider != "ipp" || o.Description != "Mock Office" || o.Location != "Lab" || !o.Caps.Color || o.Default ||
		len(o.Caps.Formats) != 1 || o.Caps.Formats[0] != "application/pdf" {
		t.Errorf("Office = %+v", o)
	}
	if uri := p.uris["Office"]; uri[:4] != "ipp:" {
		t.Errorf("Office URI %q, want plain IPP", uri)
	}
}

func TestIPPEverywherePrint(t *testing.T) {
	p, srv, browses := networkMock(t, ipptest.Printer{Name: "Office", Attrs: officeAttrs})
	c := NewClient(p)
	// Not searched yet: Print searches by itself.
	job, err := c.Print(context.Background(), pdfDoc("Report"), Settings{Provider: "ipp", Printer: "Office", Copies: 2})
	if err != nil {
		t.Fatal(err)
	}
	if *browses != 1 {
		t.Errorf("%d searches", *browses)
	}
	jobs := srv.Jobs()
	if len(jobs) != 1 || jobs[0].Printer != "Office" || len(jobs[0].Document) == 0 {
		t.Fatalf("server jobs = %+v", jobs)
	}
	if a, _ := jobs[0].Attrs.Get("copies"); a.String() != "2" {
		t.Errorf("copies = %v", a)
	}
	if _, err := job.State(context.Background()); err != nil {
		t.Errorf("State: %v", err)
	}

	caps, err := c.Capabilities(context.Background(), "ipp", "Office")
	if err != nil || !caps.Duplex || len(caps.Media) == 0 {
		t.Errorf("Capabilities = %+v, %v", caps, err)
	}
}

func TestIPPEverywhereNoPDF(t *testing.T) {
	p, srv, _ := networkMock(t, ipptest.Printer{Name: "Raster", Attrs: urfOnly})
	_, err := NewClient(p).Print(context.Background(), pdfDoc("x"), Settings{Provider: "ipp", Printer: "Raster"})
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Print = %v, want ErrUnsupported", err)
	}
	if n := len(srv.Jobs()); n != 0 {
		t.Errorf("%d jobs sent", n)
	}
}

func TestIPPEverywhereLookup(t *testing.T) {
	p, srv, browses := networkMock(t, ipptest.Printer{Name: "Office", Attrs: officeAttrs})
	c := NewClient(p)
	if _, err := c.Print(context.Background(), pdfDoc("x"), Settings{Provider: "ipp"}); !errors.Is(err, ErrNoPrinter) {
		t.Errorf("no printer name: %v", err)
	}
	if _, err := c.Print(context.Background(), pdfDoc("x"), Settings{Provider: "ipp", Printer: "Gone"}); !errors.Is(err, ErrPrinterNotFound) {
		t.Errorf("unknown printer: %v", err)
	}
	n := *browses
	uri := "ipp" + srv.URL[len("http"):] + "/printers/Office"
	if _, err := c.Print(context.Background(), pdfDoc("x"), Settings{Provider: "ipp", Printer: uri}); err != nil {
		t.Errorf("print by URI: %v", err)
	}
	if *browses != n {
		t.Errorf("printing by URI searched the network")
	}
}

func TestServiceURI(t *testing.T) {
	ll := netip.MustParseAddr("fe80::1")
	for _, tc := range []struct {
		s    dnssd.Service
		want string
	}{
		{dnssd.Service{Type: "_ipp._tcp", Host: "p.local", Port: 631, Addrs: []netip.Addr{netip.MustParseAddr("10.0.0.2")}, TXT: map[string]string{"rp": "ipp/print"}}, "ipp://10.0.0.2:631/ipp/print"},
		{dnssd.Service{Type: "_ipps._tcp", Host: "p.local", Port: 443}, "ipps://p.local:443/ipp/print"},
		{dnssd.Service{Type: "_ipp._tcp", Host: "p.local", Port: 631, Addrs: []netip.Addr{ll}, TXT: map[string]string{"rp": ""}}, "ipp://p.local:631/"},
		{dnssd.Service{Type: "_ipp._tcp", Port: 631, Addrs: []netip.Addr{netip.MustParseAddr("2001:db8::5")}}, "ipp://[2001:db8::5]:631/ipp/print"},
	} {
		if got, ok := serviceURI(tc.s); !ok || got != tc.want {
			t.Errorf("serviceURI(%+v) = %q, %v; want %q", tc.s, got, ok, tc.want)
		}
	}
	if _, ok := serviceURI(dnssd.Service{Type: "_ipp._tcp", Port: 631}); ok {
		t.Error("URI without host")
	}
}
