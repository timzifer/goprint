package virtualprinter

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint"
)

func TestInstances(t *testing.T) {
	label := Label("Label 62")
	label.Location = "Warehouse"
	s := newServer(New("v", Office("Office"), label))
	in := s.instances("pc-goprint-8631", 8631)
	if len(in) != 2 || in[1].Name != "Label 62" || in[1].Port != 8631 || in[1].Type != "_ipp._tcp" ||
		!slices.Equal(in[1].Subtypes, []string{"_print", "_universal"}) {
		t.Fatalf("instances %+v", in)
	}
	txt := strings.Join(in[1].TXT, ";")
	for _, want := range []string{"rp=printers/Label%2062", "ty=Virtual label printer", "note=Warehouse", "pdl=application/pdf", "Color=F", "Duplex=F", "txtvers=1"} {
		if !strings.Contains(txt, want) {
			t.Errorf("TXT %q lacks %q", txt, want)
		}
	}
	if !strings.Contains(strings.Join(in[0].TXT, ";"), "Duplex=T") {
		t.Errorf("Office TXT %v", in[0].TXT)
	}
	if a, b := s.instances("h", 1)[0].TXT, s.instances("h", 1)[0].TXT; !slices.Equal(a, b) {
		t.Error("UUID not stable")
	}
}

func TestHostLabel(t *testing.T) {
	l := hostLabel()
	if l == "" || strings.ContainsAny(l, ". _") || strings.ToLower(l) != l {
		t.Errorf("hostLabel() = %q", l)
	}
}

// TestAdvertiseFindAndPrint announces a printer on the network, finds it
// with goprint's IPPEverywhere provider and prints to it. It binds port
// 5353 on all interfaces; set GOPRINT_MDNS=1.
func TestAdvertiseFindAndPrint(t *testing.T) {
	if os.Getenv("GOPRINT_MDNS") == "" {
		t.Skip("set GOPRINT_MDNS=1 to use multicast DNS on the network")
	}
	// No dots: DNS-SD instance names get hyphens for them.
	name := "goprint test " + time.Now().Format("150405000")
	vp := New("v", Office(name))
	srv, err := vp.Listen(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Advertise(); err != nil {
		t.Fatal(err)
	}
	c := goprint.NewClient(goprint.IPPEverywhere(goprint.IPPEverywhereOptions{BrowseTimeout: 3 * time.Second}))
	ps, err := c.Printers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ps, func(p goprint.Printer) bool { return p.Name == name })
	if i < 0 {
		t.Fatalf("%q not found among %d printers", name, len(ps))
	}
	if p := ps[i]; p.Description != "Virtual office printer" || !p.Caps.Duplex || !slices.Contains(p.Caps.Formats, "application/pdf") {
		t.Errorf("found %+v", p)
	}
	if _, err := c.Print(context.Background(), goprint.PDFBytes("Over mDNS", a4PDF()), goprint.Settings{Provider: "ipp", Printer: name}); err != nil {
		t.Fatal(err)
	}
	if jobs := vp.Jobs(); len(jobs) != 1 || jobs[0].Title != "Over mDNS" {
		t.Errorf("jobs %+v", jobs)
	}
}
