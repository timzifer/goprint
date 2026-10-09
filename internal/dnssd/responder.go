package dnssd

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// Instance is a service instance a [Responder] announces.
type Instance struct {
	// Name is the instance name, e.g. "Office"; dots are replaced by
	// hyphens, as names are encoded without escapes.
	Name string
	// Type is the service type, e.g. "_ipp._tcp".
	Type string
	// Subtypes are announced as <subtype>._sub.<type>, e.g. "_print".
	Subtypes []string
	Port     int
	// TXT holds "key=value" strings.
	TXT []string
}

// TTLs of RFC 6762 10: host records 120 s, the others 75 min; answers to
// legacy unicast queries at most 10 s.
const (
	ttlHost   = 120
	ttlOther  = 4500
	ttlLegacy = 10
	// cacheFlush marks unique records (SRV, TXT, A) in multicast answers.
	cacheFlush = 1 << 15
)

// zone answers questions about the instances of a host.
type zone struct {
	host      string              // "goprint-631.local."
	instances func() []Instance   // current instances
	addrs     func() []netip.Addr // the host's addresses
}

func fqdn(s string) string {
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func instanceName(in Instance) string {
	return strings.ReplaceAll(in.Name, ".", "-") + "." + fqdn(in.Type+".local")
}

// records are the resources of the zone, built for one answer.
type records struct {
	ttl func(base uint32) uint32
	cls dnsmessage.Class // class of unique records
	out []dnsmessage.Resource
}

func (r *records) add(name string, ttl uint32, unique bool, body dnsmessage.ResourceBody) {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		return
	}
	cls := dnsmessage.ClassINET
	if unique {
		cls = r.cls
	}
	for _, have := range r.out {
		if have.Header.Name == n && have.Body.GoString() == body.GoString() {
			return
		}
	}
	var t dnsmessage.Type
	switch body.(type) {
	case *dnsmessage.PTRResource:
		t = dnsmessage.TypePTR
	case *dnsmessage.SRVResource:
		t = dnsmessage.TypeSRV
	case *dnsmessage.TXTResource:
		t = dnsmessage.TypeTXT
	case *dnsmessage.AResource:
		t = dnsmessage.TypeA
	case *dnsmessage.AAAAResource:
		t = dnsmessage.TypeAAAA
	}
	r.out = append(r.out, dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: n, Type: t, Class: cls, TTL: r.ttl(ttl)},
		Body:   body,
	})
}

func mustName(s string) dnsmessage.Name {
	n, err := dnsmessage.NewName(s)
	if err != nil {
		return dnsmessage.MustNewName("invalid.local.")
	}
	return n
}

func (z *zone) ptr(r *records, name, target string) {
	r.add(name, ttlOther, false, &dnsmessage.PTRResource{PTR: mustName(target)})
}

func (z *zone) srvTXT(r *records, in Instance) {
	full := instanceName(in)
	r.add(full, ttlOther, true, &dnsmessage.SRVResource{Target: mustName(z.host), Port: uint16(in.Port)})
	txt := in.TXT
	if len(txt) == 0 {
		txt = []string{""}
	}
	r.add(full, ttlOther, true, &dnsmessage.TXTResource{TXT: txt})
}

func (z *zone) address(r *records, v4, v6 bool) {
	for _, a := range z.addrs() {
		switch {
		case a.Is4() && v4:
			r.add(z.host, ttlHost, true, &dnsmessage.AResource{A: a.As4()})
		case !a.Is4() && v6:
			r.add(z.host, ttlHost, true, &dnsmessage.AAAAResource{AAAA: a.As16()})
		}
	}
}

// answer returns the response to q, and false if the zone has nothing to
// say. legacy marks a query from a port other than 5353 (RFC 6762 6.7):
// it gets the question back, its id, short TTLs and no cache-flush bits.
func (z *zone) answer(q *dnsmessage.Message, legacy bool) (*dnsmessage.Message, bool) {
	ans := &records{ttl: func(t uint32) uint32 { return t }, cls: dnsmessage.ClassINET | cacheFlush}
	add := &records{ttl: ans.ttl, cls: ans.cls}
	if legacy {
		short := func(t uint32) uint32 { return min(t, ttlLegacy) }
		ans = &records{ttl: short, cls: dnsmessage.ClassINET}
		add = &records{ttl: short, cls: dnsmessage.ClassINET}
	}
	instances := z.instances()
	for _, qu := range q.Questions {
		name := strings.ToLower(qu.Name.String())
		t := qu.Type
		all := t == dnsmessage.TypeALL
		if name == "_services._dns-sd._udp.local." && (t == dnsmessage.TypePTR || all) {
			for _, in := range instances {
				z.ptr(ans, name, fqdn(in.Type+".local"))
			}
		}
		for _, in := range instances {
			typ := strings.ToLower(fqdn(in.Type + ".local"))
			full := strings.ToLower(instanceName(in))
			browse := name == typ
			for _, st := range in.Subtypes {
				browse = browse || name == strings.ToLower(st+"._sub."+typ)
			}
			switch {
			case browse && (t == dnsmessage.TypePTR || all):
				z.ptr(ans, qu.Name.String(), instanceName(in))
				z.srvTXT(add, in)
				z.address(add, true, true)
			case name == full && (t == dnsmessage.TypeSRV || t == dnsmessage.TypeTXT || all):
				z.srvTXT(ans, in)
				z.address(add, true, true)
			}
		}
		if name == strings.ToLower(z.host) {
			z.address(ans, t == dnsmessage.TypeA || all, t == dnsmessage.TypeAAAA || all)
		}
	}
	if len(ans.out) == 0 {
		return nil, false
	}
	m := &dnsmessage.Message{
		Header:      dnsmessage.Header{Response: true, Authoritative: true},
		Answers:     ans.out,
		Additionals: add.out,
	}
	if legacy {
		m.Header.ID = q.Header.ID
		m.Questions = q.Questions
	}
	return m, true
}

// announcement returns all records of the zone as an unsolicited
// response; goodbye sets their TTL to 0 (RFC 6762 10.1).
func (z *zone) announcement(goodbye bool) *dnsmessage.Message {
	r := &records{ttl: func(t uint32) uint32 { return t }, cls: dnsmessage.ClassINET | cacheFlush}
	if goodbye {
		r.ttl = func(uint32) uint32 { return 0 }
	}
	for _, in := range z.instances() {
		typ := fqdn(in.Type + ".local")
		z.ptr(r, typ, instanceName(in))
		for _, st := range in.Subtypes {
			z.ptr(r, st+"._sub."+typ, instanceName(in))
		}
		z.ptr(r, "_services._dns-sd._udp.local.", typ)
		z.srvTXT(r, in)
	}
	z.address(r, true, true)
	return &dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true}, Answers: r.out}
}

// Responder answers multicast DNS queries for its instances on port 5353,
// next to the system's responder, until Close.
type Responder struct {
	z      *zone
	conn   *net.UDPConn
	pc     *ipv4.PacketConn
	ifaces []net.Interface
	done   chan struct{}
	once   sync.Once
}

// Respond starts a responder for the instances on host (e.g.
// "goprint-631"; ".local" is added). instances is asked on every query,
// so the responder follows changes. The host's addresses are those of
// the machine's interfaces.
func Respond(host string, instances func() []Instance) (*Responder, error) {
	conn, err := net.ListenMulticastUDP("udp4", nil, ipv4Group)
	if err != nil {
		return nil, err
	}
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetMulticastTTL(255)
	_ = pc.SetMulticastLoopback(true)
	r := &Responder{
		z: &zone{
			host:      fqdn(strings.TrimSuffix(strings.TrimSuffix(host, "."), ".local") + ".local"),
			instances: instances,
			addrs:     hostAddrs,
		},
		conn: conn, pc: pc, done: make(chan struct{}),
	}
	ifaces, _ := net.Interfaces()
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		// Joining fails where the default join already covered it.
		_ = pc.JoinGroup(&ifi, ipv4Group)
		r.ifaces = append(r.ifaces, ifi)
	}
	go r.serve()
	go r.announce()
	return r, nil
}

// hostAddrs are the IPv4 addresses of the interfaces, without loopback.
func hostAddrs() []netip.Addr {
	var out []netip.Addr
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.Is4() && !ip.IsLoopback() {
			out = append(out, ip)
		}
	}
	return out
}

func (r *Responder) serve() {
	buf := make([]byte, 9000)
	for {
		n, src, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		var q dnsmessage.Message
		if q.Unpack(buf[:n]) != nil || q.Header.Response {
			continue
		}
		legacy := src.Port != 5353
		resp, ok := r.z.answer(&q, legacy)
		if !ok {
			continue
		}
		b, err := resp.Pack()
		if err != nil {
			continue
		}
		if legacy {
			_, _ = r.conn.WriteToUDP(b, src)
		} else {
			r.multicast(b)
		}
	}
}

// announce sends the records twice, a second apart (RFC 6762 8.3).
func (r *Responder) announce() {
	for i := 0; i < 2; i++ {
		if b, err := r.z.announcement(false).Pack(); err == nil {
			r.multicast(b)
		}
		select {
		case <-r.done:
			return
		case <-time.After(time.Second):
		}
	}
}

func (r *Responder) multicast(b []byte) {
	sent := false
	for _, ifi := range r.ifaces {
		if r.pc.SetMulticastInterface(&ifi) != nil {
			continue
		}
		if _, err := r.pc.WriteTo(b, nil, ipv4Group); err == nil {
			sent = true
		}
	}
	if !sent {
		_, _ = r.conn.WriteToUDP(b, ipv4Group)
	}
}

// Close sends goodbye records and stops the responder.
func (r *Responder) Close() error {
	var err error
	r.once.Do(func() {
		if b, e := r.z.announcement(true).Pack(); e == nil {
			r.multicast(b)
		}
		close(r.done)
		err = r.conn.Close()
	})
	return err
}
