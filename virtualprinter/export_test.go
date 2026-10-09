package virtualprinter

import (
	"context"
	"fmt"
	"time"

	"github.com/timzifer/goprint/internal/dnssd"
)

// browseForTest lists the IPP services on the network, for diagnosis.
func browseForTest() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ss, err := dnssd.Browse(ctx, "_ipp._tcp", "_ipps._tcp")
	var out []string
	for _, s := range ss {
		out = append(out, fmt.Sprintf("%s %q %s:%d %v rp=%q", s.Type, s.Instance, s.Host, s.Port, s.Addrs, s.TXT["rp"]))
	}
	return out, err
}
