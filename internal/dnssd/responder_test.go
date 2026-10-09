package dnssd

import (
	"context"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func testZone() *zone {
	return &zone{
		host: "goprint-631.local.",
		instances: func() []Instance {
			return []Instance{{Name: "Office v1.2", Type: "_ipp._tcp", Subtypes: []string{"_print"}, Port: 631, TXT: []string{"rp=printers/Office", "pdl=application/pdf"}}}
		},
		addrs: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.9")} },
	}
}

func query(id uint16, name string, t dnsmessage.Type) *dnsmessage.Message {
	return &dnsmessage.Message{Header: dnsmessage.Header{ID: id},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: t, Class: dnsmessage.ClassINET}}}
}

func types(rs []dnsmessage.Resource) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Header.Type.String()+" "+r.Header.Name.String())
	}
	return out
}

func TestZoneBrowse(t *testing.T) {
	z := testZone()
	for _, name := range []string{"_ipp._tcp.local.", "_print._sub._ipp._tcp.local.", "_IPP._TCP.local."} {
		resp, ok := z.answer(query(7, name, dnsmessage.TypePTR), false)
		if !ok {
			t.Fatalf("%s: no answer", name)
		}
		if len(resp.Answers) != 1 || resp.Answers[0].Body.(*dnsmessage.PTRResource).PTR.String() != "Office v1-2._ipp._tcp.local." {
			t.Errorf("%s: answers %v", name, types(resp.Answers))
		}
		if got := types(resp.Additionals); !slices.Equal(got, []string{
			"TypeSRV Office v1-2._ipp._tcp.local.", "TypeTXT Office v1-2._ipp._tcp.local.", "TypeA goprint-631.local.",
		}) {
			t.Errorf("%s: additionals %v", name, got)
		}
		// Multicast answers: id 0, no question, cache-flush on unique records.
		if resp.Header.ID != 0 || len(resp.Questions) != 0 || resp.Additionals[0].Header.Class&cacheFlush == 0 {
			t.Errorf("%s: header %+v, %d questions, SRV class %v", name, resp.Header, len(resp.Questions), resp.Additionals[0].Header.Class)
		}
	}
}

func TestZoneLegacyUnicast(t *testing.T) {
	z := testZone()
	resp, ok := z.answer(query(42, "Office v1-2._ipp._tcp.local.", dnsmessage.TypeSRV), true)
	if !ok {
		t.Fatal("no answer")
	}
	if resp.Header.ID != 42 || len(resp.Questions) != 1 {
		t.Errorf("legacy answer without id or question: %+v", resp.Header)
	}
	srv := resp.Answers[0]
	if srv.Header.TTL > ttlLegacy || srv.Header.Class != dnsmessage.ClassINET || srv.Body.(*dnsmessage.SRVResource).Port != 631 {
		t.Errorf("SRV %+v", srv)
	}
	if _, err := resp.Pack(); err != nil {
		t.Error(err)
	}
}

func TestZoneHostAndMisses(t *testing.T) {
	z := testZone()
	resp, ok := z.answer(query(1, "goprint-631.local.", dnsmessage.TypeA), false)
	if !ok || len(resp.Answers) != 1 || resp.Answers[0].Body.(*dnsmessage.AResource).A != [4]byte{192, 168, 1, 9} {
		t.Errorf("A: %v", resp)
	}
	if _, ok := z.answer(query(1, "goprint-631.local.", dnsmessage.TypeAAAA), false); ok {
		t.Error("answered AAAA without IPv6 address")
	}
	if _, ok := z.answer(query(1, "_http._tcp.local.", dnsmessage.TypePTR), false); ok {
		t.Error("answered a foreign service")
	}
	resp, ok = z.answer(query(1, "_services._dns-sd._udp.local.", dnsmessage.TypePTR), false)
	if !ok || resp.Answers[0].Body.(*dnsmessage.PTRResource).PTR.String() != "_ipp._tcp.local." {
		t.Errorf("service enumeration: %v", resp)
	}
}

func TestZoneAnnouncement(t *testing.T) {
	z := testZone()
	m := z.announcement(false)
	if got := types(m.Answers); len(got) != 6 {
		t.Errorf("announcement %v", got)
	}
	for _, r := range z.announcement(true).Answers {
		if r.Header.TTL != 0 {
			t.Errorf("goodbye with TTL %d: %v", r.Header.TTL, r.Header.Name)
		}
	}
	if _, err := m.Pack(); err != nil {
		t.Error(err)
	}
}

// TestRespondAndBrowse runs a responder on the network and finds it with
// Browse. It binds port 5353 on all interfaces; set GOPRINT_MDNS=1.
func TestRespondAndBrowse(t *testing.T) {
	if os.Getenv("GOPRINT_MDNS") == "" {
		t.Skip("set GOPRINT_MDNS=1 to use multicast DNS on the network")
	}
	r, err := Respond("goprint-test-"+time.Now().Format("150405"), func() []Instance {
		return []Instance{{Name: "goprint mdns test", Type: "_ipp._tcp", Port: 8631, TXT: []string{"rp=printers/x"}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ss, err := Browse(ctx, "_ipp._tcp")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ss, func(s Service) bool { return s.Instance == "goprint mdns test" })
	if i < 0 {
		t.Fatalf("not found among %+v", ss)
	}
	if s := ss[i]; s.Port != 8631 || s.TXT["rp"] != "printers/x" || len(s.Addrs) == 0 {
		t.Errorf("found %+v", s)
	}
}

func TestRespondRejectsLongHost(t *testing.T) {
	if _, err := Respond(strings.Repeat("a", 64), func() []Instance { return nil }); err == nil {
		t.Error("64-character host label accepted")
	}
}
