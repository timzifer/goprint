// Package dnssd browses DNS-SD services over multicast DNS (RFC 6762,
// RFC 6763), just enough to find IPP printers on the local network.
//
// Queries are sent from an ephemeral port as "legacy unicast" queries
// (RFC 6762 6.7): responders answer to that port directly, so port 5353
// stays with the system's own responder (Avahi, Bonjour, Windows).
package dnssd

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Service is a discovered service instance.
type Service struct {
	// Instance is the instance name, e.g. "Office Printer".
	Instance string
	// Type is the service type, e.g. "_ipp._tcp".
	Type string
	// Host is the target host, e.g. "printer.local".
	Host string
	Port int
	// Addrs are the host's addresses, IPv4 first.
	Addrs []netip.Addr
	// TXT holds the TXT record; keys are lower case.
	TXT map[string]string
}

// transport sends queries and receives responses.
type transport interface {
	send(b []byte) error
	// recv reads one response; it returns os.ErrDeadlineExceeded when the
	// deadline passes.
	recv(b []byte, deadline time.Time) (int, error)
	close() error
}

// requeryInterval is the pause between rounds of queries.
const requeryInterval = 300 * time.Millisecond

// Browse queries the service types (e.g. "_ipp._tcp") until ctx is done
// and returns the instances found, sorted by type and instance name. ctx
// must have a deadline. Only an error before the first query is reported;
// later network errors end the browse early.
func Browse(ctx context.Context, types ...string) ([]Service, error) {
	tr, err := newMulticast()
	if err != nil {
		return nil, err
	}
	defer tr.close()
	return browse(ctx, tr, types)
}

func browse(ctx context.Context, tr transport, types []string) ([]Service, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("dnssd: browse needs a deadline")
	}
	st := newState(types)
	buf := make([]byte, 9000)
	next := time.Now()
	for round := 0; ; {
		if now := time.Now(); !now.Before(next) {
			if q := st.query(round < 2); q != nil {
				if err := tr.send(q); err != nil && round == 0 {
					return nil, err
				}
			}
			round++
			next = now.Add(requeryInterval)
		}
		wait := next
		if deadline.Before(wait) {
			wait = deadline
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			break
		}
		n, err := tr.recv(buf, wait)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			continue
		}
		if err != nil {
			break
		}
		st.add(buf[:n])
	}
	return st.services(), nil
}

// state collects the records of a browse.
type state struct {
	types map[string]string // "_ipp._tcp.local." → "_ipp._tcp"

	instances map[string]instance // full instance name (lower case) → instance
	hosts     map[string][]netip.Addr
}

type instance struct {
	name, typ string // display name, service type
	full      string // full name as received
	host      string
	port      int
	txt       map[string]string
	hasSRV    bool
	hasTXT    bool
}

func newState(types []string) *state {
	s := &state{types: map[string]string{}, instances: map[string]instance{}, hosts: map[string][]netip.Addr{}}
	for _, t := range types {
		t = strings.TrimSuffix(t, ".")
		s.types[strings.ToLower(t)+".local."] = t
	}
	return s
}

// query builds the next query: the service types if browse is set, and
// whatever records are still missing for the instances found so far.
func (s *state) query(browse bool) []byte {
	var qs []dnsmessage.Question
	ask := func(name string, t dnsmessage.Type) {
		n, err := dnsmessage.NewName(name)
		if err != nil {
			return
		}
		qs = append(qs, dnsmessage.Question{Name: n, Type: t, Class: dnsmessage.ClassINET})
	}
	if browse {
		for full := range s.types {
			ask(full, dnsmessage.TypePTR)
		}
	}
	for _, in := range s.instances {
		// Names with dots in a label cannot be asked for; their records
		// arrive with the PTR answer or not at all.
		if strings.Count(in.full, ".") != strings.Count(in.typ, ".")+3 {
			continue
		}
		if !in.hasSRV {
			ask(in.full, dnsmessage.TypeSRV)
		}
		if !in.hasTXT {
			ask(in.full, dnsmessage.TypeTXT)
		}
		if in.hasSRV && len(s.hosts[strings.ToLower(in.host)]) == 0 {
			ask(in.host, dnsmessage.TypeA)
			ask(in.host, dnsmessage.TypeAAAA)
		}
	}
	if len(qs) == 0 {
		return nil
	}
	slices.SortFunc(qs, func(a, b dnsmessage.Question) int { return strings.Compare(a.Name.String(), b.Name.String()) })
	m := dnsmessage.Message{Questions: qs}
	b, err := m.Pack()
	if err != nil {
		return nil
	}
	return b
}

// add takes the records of a response.
func (s *state) add(b []byte) {
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil || !m.Response {
		return
	}
	for _, r := range slices.Concat(m.Answers, m.Additionals) {
		if r.Header.TTL == 0 {
			continue // goodbye
		}
		name := r.Header.Name.String()
		key := strings.ToLower(name)
		switch body := r.Body.(type) {
		case *dnsmessage.PTRResource:
			typ, ok := s.types[key]
			if !ok {
				continue
			}
			full := body.PTR.String()
			inst, ok := strings.CutSuffix(strings.ToLower(full), "."+key)
			if !ok || inst == "" {
				continue
			}
			in := s.instance(full, typ)
			in.name = full[:len(inst)]
			s.instances[strings.ToLower(full)] = in
		case *dnsmessage.SRVResource:
			in, ok := s.known(name)
			if !ok {
				continue
			}
			in.host, in.port, in.hasSRV = body.Target.String(), int(body.Port), true
			s.instances[key] = in
		case *dnsmessage.TXTResource:
			in, ok := s.known(name)
			if !ok {
				continue
			}
			in.txt, in.hasTXT = parseTXT(body.TXT), true
			s.instances[key] = in
		case *dnsmessage.AResource:
			s.addAddr(key, netip.AddrFrom4(body.A))
		case *dnsmessage.AAAAResource:
			s.addAddr(key, netip.AddrFrom16(body.AAAA))
		}
	}
}

// instance returns the instance with the full name, new if unknown.
func (s *state) instance(full, typ string) instance {
	if in, ok := s.instances[strings.ToLower(full)]; ok {
		return in
	}
	return instance{full: full, typ: typ}
}

// known returns the instance a SRV or TXT record belongs to. Records of
// instances not seen in a PTR answer yet are kept if their type is browsed.
func (s *state) known(name string) (instance, bool) {
	key := strings.ToLower(name)
	if in, ok := s.instances[key]; ok {
		return in, true
	}
	for full, typ := range s.types {
		if inst, ok := strings.CutSuffix(key, "."+full); ok && inst != "" {
			in := instance{full: name, typ: typ, name: name[:len(inst)]}
			s.instances[key] = in
			return in, true
		}
	}
	return instance{}, false
}

func (s *state) addAddr(host string, a netip.Addr) {
	if !slices.Contains(s.hosts[host], a) {
		s.hosts[host] = append(s.hosts[host], a)
	}
}

// services returns the instances with a known host and port.
func (s *state) services() []Service {
	var out []Service
	for _, in := range s.instances {
		if !in.hasSRV {
			continue
		}
		addrs := slices.Clone(s.hosts[strings.ToLower(in.host)])
		slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
			switch {
			case a.Is4() && !b.Is4():
				return -1
			case !a.Is4() && b.Is4():
				return 1
			}
			return 0
		})
		out = append(out, Service{
			Instance: in.name,
			Type:     in.typ,
			Host:     strings.TrimSuffix(in.host, "."),
			Port:     in.port,
			Addrs:    addrs,
			TXT:      in.txt,
		})
	}
	slices.SortFunc(out, func(a, b Service) int {
		if c := strings.Compare(a.Type, b.Type); c != 0 {
			return c
		}
		return strings.Compare(a.Instance, b.Instance)
	})
	return out
}

// parseTXT splits "key=value" strings; keys are case-insensitive
// (RFC 6763 6.4). The first occurrence of a key wins.
func parseTXT(txt []string) map[string]string {
	m := make(map[string]string, len(txt))
	for _, kv := range txt {
		k, v, _ := strings.Cut(kv, "=")
		k = strings.ToLower(k)
		if _, dup := m[k]; k != "" && !dup {
			m[k] = v
		}
	}
	return m
}

// ipv4Group is the mDNS IPv4 multicast group.
var ipv4Group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
