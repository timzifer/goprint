package dnssd

import (
	"errors"
	"net"
	"time"

	"golang.org/x/net/ipv4"
)

// multicast sends queries to the IPv4 mDNS group on every multicast
// interface and receives the unicast answers on an ephemeral port.
type multicast struct {
	conn   net.PacketConn
	pc     *ipv4.PacketConn
	ifaces []net.Interface
}

func newMulticast() (*multicast, error) {
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetMulticastTTL(255) // RFC 6762 11
	_ = pc.SetMulticastLoopback(true)
	m := &multicast{conn: conn, pc: pc}
	ifaces, _ := net.Interfaces()
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagMulticast != 0 {
			m.ifaces = append(m.ifaces, ifi)
		}
	}
	return m, nil
}

func (m *multicast) send(b []byte) error {
	var errs []error
	sent := false
	for _, ifi := range m.ifaces {
		if err := m.pc.SetMulticastInterface(&ifi); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := m.pc.WriteTo(b, nil, ipv4Group); err != nil {
			errs = append(errs, err)
			continue
		}
		sent = true
	}
	if !sent {
		// No usable interface list: let the routing table pick one.
		if _, err := m.conn.WriteTo(b, ipv4Group); err != nil {
			return errors.Join(append(errs, err)...)
		}
	}
	return nil
}

func (m *multicast) recv(b []byte, deadline time.Time) (int, error) {
	if err := m.conn.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	n, _, err := m.conn.ReadFrom(b)
	return n, err
}

func (m *multicast) close() error { return m.conn.Close() }
