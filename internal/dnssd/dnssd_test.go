package dnssd

import (
	"context"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeNet is a transport that answers queries with a responder function,
// like printers on the network would.
type fakeNet struct {
	mu      sync.Mutex
	respond func(q dnsmessage.Message) [][]dnsmessage.Resource // one packet per element
	pending [][]byte
	queries []dnsmessage.Message
	ready   chan struct{}
}

func newFakeNet(respond func(q dnsmessage.Message) [][]dnsmessage.Resource) *fakeNet {
	return &fakeNet{respond: respond, ready: make(chan struct{}, 64)}
}

func (f *fakeNet) send(b []byte) error {
	var q dnsmessage.Message
	if err := q.Unpack(b); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	for _, rs := range f.respond(q) {
		m := dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true}, Questions: q.Questions, Answers: rs}
		p, err := m.Pack()
		if err != nil {
			return err
		}
		f.pending = append(f.pending, p)
		f.ready <- struct{}{}
	}
	return nil
}

func (f *fakeNet) recv(b []byte, deadline time.Time) (int, error) {
	select {
	case <-f.ready:
	case <-time.After(time.Until(deadline)):
		return 0, os.ErrDeadlineExceeded
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pending[0]
	f.pending = f.pending[1:]
	return copy(b, p), nil
}

func (f *fakeNet) close() error { return nil }

func name(s string) dnsmessage.Name { return dnsmessage.MustNewName(s) }

func hdr(n string, t dnsmessage.Type, ttl uint32) dnsmessage.ResourceHeader {
	return dnsmessage.ResourceHeader{Name: name(n), Type: t, Class: dnsmessage.ClassINET, TTL: ttl}
}

func ptr(typ, inst string) dnsmessage.Resource {
	return dnsmessage.Resource{Header: hdr(typ, dnsmessage.TypePTR, 120), Body: &dnsmessage.PTRResource{PTR: name(inst)}}
}

func srv(inst, host string, port uint16) dnsmessage.Resource {
	return dnsmessage.Resource{Header: hdr(inst, dnsmessage.TypeSRV, 120), Body: &dnsmessage.SRVResource{Target: name(host), Port: port}}
}

func txt(inst string, kv ...string) dnsmessage.Resource {
	return dnsmessage.Resource{Header: hdr(inst, dnsmessage.TypeTXT, 120), Body: &dnsmessage.TXTResource{TXT: kv}}
}

func a(host string, ip string) dnsmessage.Resource {
	return dnsmessage.Resource{Header: hdr(host, dnsmessage.TypeA, 120), Body: &dnsmessage.AResource{A: netip.MustParseAddr(ip).As4()}}
}

func asked(q dnsmessage.Message, n string, t dnsmessage.Type) bool {
	for _, x := range q.Questions {
		if strings.EqualFold(x.Name.String(), n) && x.Type == t {
			return true
		}
	}
	return false
}

func run(t *testing.T, f *fakeNet, types ...string) []Service {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	got, err := browse(ctx, f, types)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBrowseAllInOne(t *testing.T) {
	const inst = "Office Printer._ipp._tcp.local."
	f := newFakeNet(func(q dnsmessage.Message) [][]dnsmessage.Resource {
		if !asked(q, "_ipp._tcp.local.", dnsmessage.TypePTR) {
			return nil
		}
		return [][]dnsmessage.Resource{{
			ptr("_ipp._tcp.local.", inst),
			srv(inst, "office.local.", 631),
			txt(inst, "rp=ipp/print", "ty=ACME Laser 9000", "pdl=application/pdf,image/urf", "Color=T", "rp=ignored"),
			a("office.local.", "192.168.1.20"),
			// Not browsed:
			ptr("_http._tcp.local.", "Web._http._tcp.local."),
		}}
	})
	got := run(t, f, "_ipp._tcp")
	want := []Service{{
		Instance: "Office Printer",
		Type:     "_ipp._tcp",
		Host:     "office.local",
		Port:     631,
		Addrs:    []netip.Addr{netip.MustParseAddr("192.168.1.20")},
		TXT:      map[string]string{"rp": "ipp/print", "ty": "ACME Laser 9000", "pdl": "application/pdf,image/urf", "color": "T"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestBrowseFollowUpQueries(t *testing.T) {
	// The responder only answers what is asked, so the browse must ask for
	// SRV, TXT and the address itself.
	const inst = "Lab._ipps._tcp.local."
	f := newFakeNet(func(q dnsmessage.Message) [][]dnsmessage.Resource {
		var out [][]dnsmessage.Resource
		if asked(q, "_ipps._tcp.local.", dnsmessage.TypePTR) {
			out = append(out, []dnsmessage.Resource{ptr("_ipps._tcp.local.", inst)})
		}
		if asked(q, inst, dnsmessage.TypeSRV) {
			out = append(out, []dnsmessage.Resource{srv(inst, "lab.local.", 443)})
		}
		if asked(q, inst, dnsmessage.TypeTXT) {
			out = append(out, []dnsmessage.Resource{txt(inst, "RP=ipp/print")})
		}
		if asked(q, "lab.local.", dnsmessage.TypeA) {
			out = append(out, []dnsmessage.Resource{a("lab.local.", "10.0.0.7")})
		}
		return out
	})
	got := run(t, f, "_ipp._tcp", "_ipps._tcp")
	if len(got) != 1 || got[0].Instance != "Lab" || got[0].Type != "_ipps._tcp" || got[0].Port != 443 ||
		got[0].TXT["rp"] != "ipp/print" || len(got[0].Addrs) != 1 || got[0].Addrs[0].String() != "10.0.0.7" {
		t.Fatalf("got %+v", got)
	}
}

func TestBrowseIgnoresGoodbyeAndIncomplete(t *testing.T) {
	f := newFakeNet(func(q dnsmessage.Message) [][]dnsmessage.Resource {
		if !asked(q, "_ipp._tcp.local.", dnsmessage.TypePTR) {
			return nil
		}
		gone := ptr("_ipp._tcp.local.", "Gone._ipp._tcp.local.")
		gone.Header.TTL = 0
		return [][]dnsmessage.Resource{{gone, ptr("_ipp._tcp.local.", "NoSRV._ipp._tcp.local.")}}
	})
	if got := run(t, f, "_ipp._tcp"); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	// NoSRV was asked for again; Gone was not.
	var srvAsked, goneAsked bool
	for _, q := range f.queries {
		srvAsked = srvAsked || asked(q, "NoSRV._ipp._tcp.local.", dnsmessage.TypeSRV)
		goneAsked = goneAsked || asked(q, "Gone._ipp._tcp.local.", dnsmessage.TypeSRV)
	}
	if !srvAsked || goneAsked {
		t.Errorf("SRV asked for NoSRV %v, for Gone %v", srvAsked, goneAsked)
	}
}

func TestBrowseNeedsDeadline(t *testing.T) {
	if _, err := browse(context.Background(), newFakeNet(nil), []string{"_ipp._tcp"}); err == nil {
		t.Error("no error without deadline")
	}
}

func TestParseTXT(t *testing.T) {
	got := parseTXT([]string{"A=1", "a=2", "flag", "=x", "b="})
	want := map[string]string{"a": "1", "flag": "", "b": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTXT = %v", got)
	}
}
