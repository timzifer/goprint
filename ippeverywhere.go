package goprint

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timzifer/goprint/internal/dnssd"
	"github.com/timzifer/goprint/ipp"
)

// IPPEverywhereOptions configures [IPPEverywhere].
type IPPEverywhereOptions struct {
	// Name is the provider name; empty means "ipp".
	Name string
	// BrowseTimeout bounds the network search of Printers; 0 means two
	// seconds. A shorter deadline of the caller's context wins.
	BrowseTimeout time.Duration
	// Rasterizer renders PDF for printers that accept no PDF but PWG
	// Raster or Apple Raster, as many network printers do. Nil leaves
	// them out: Print returns ErrUnsupported. The module
	// github.com/timzifer/goprint/raster provides one.
	Rasterizer Rasterizer
}

// IPPEverywhere returns a provider for the IPP printers on the local
// network (IPP Everywhere, AirPrint, Mopria), found through DNS-SD
// (multicast DNS) without a driver or print server. It works on every
// platform, also on Linux without CUPS.
//
// Printers are named by their DNS-SD instance name; there is no default
// printer. Print also takes a printer URI ("ipp://…") as name. PDF is
// sent as it is to printers that accept it; for the others it is
// rendered by [IPPEverywhereOptions.Rasterizer].
func IPPEverywhere(opts IPPEverywhereOptions) Provider {
	if opts.Name == "" {
		opts.Name = "ipp"
	}
	if opts.BrowseTimeout <= 0 {
		opts.BrowseTimeout = 2 * time.Second
	}
	return &ippEverywhere{opts: opts, browse: dnssd.Browse, uris: map[string]string{}}
}

type ippEverywhere struct {
	opts   IPPEverywhereOptions
	browse func(ctx context.Context, types ...string) ([]dnssd.Service, error)

	mu   sync.Mutex
	uris map[string]string // printer name → URI, from the last browse
}

// networkIPP prints to printer URIs; it has no default server.
var networkIPP = ippBackend{newClient: func(...ipp.Option) (*ipp.Client, error) {
	return nil, fmt.Errorf("%w: no default printer on the network", ErrNoPrinter)
}}

func (p *ippEverywhere) Name() string { return p.opts.Name }

func (p *ippEverywhere) Printers(ctx context.Context) ([]Printer, error) {
	ctx, cancel := context.WithTimeout(ctx, p.opts.BrowseTimeout)
	defer cancel()
	services, err := p.browse(ctx, "_ipp._tcp", "_ipps._tcp")
	if err != nil {
		return nil, fmt.Errorf("goprint: searching network printers: %w", err)
	}
	// One printer per instance; plain IPP wins over IPPS, whose
	// self-signed certificates the default TLS setup rejects.
	byName := map[string]dnssd.Service{}
	for _, s := range services {
		if old, ok := byName[s.Instance]; !ok || (old.Type == "_ipps._tcp" && s.Type == "_ipp._tcp") {
			byName[s.Instance] = s
		}
	}
	var out []Printer
	uris := map[string]string{}
	for name, s := range byName {
		uri, ok := serviceURI(s)
		if !ok {
			continue
		}
		uris[name] = uri
		out = append(out, printerFromService(s))
	}
	slices.SortFunc(out, func(a, b Printer) int { return strings.Compare(a.Name, b.Name) })
	p.mu.Lock()
	p.uris = uris
	p.mu.Unlock()
	return out, nil
}

// serviceURI returns the printer URI of an IPP service.
func serviceURI(s dnssd.Service) (string, bool) {
	scheme := "ipp"
	if s.Type == "_ipps._tcp" {
		scheme = "ipps"
	}
	host := s.Host
	if i := slices.IndexFunc(s.Addrs, func(a netip.Addr) bool { return a.Is4() || !a.IsLinkLocalUnicast() }); i >= 0 {
		// The address, as not every platform resolves .local names.
		host = s.Addrs[i].String()
	}
	if host == "" || s.Port <= 0 {
		return "", false
	}
	rp := strings.Trim(s.TXT["rp"], "/")
	if _, ok := s.TXT["rp"]; !ok {
		rp = "ipp/print"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(s.Port)) + "/" + rp, true
}

// printerFromService describes a printer from its DNS-SD record (keys of
// the IPP Everywhere / AirPrint TXT record). The full capabilities come
// from the printer itself through Capabilities.
func printerFromService(s dnssd.Service) Printer {
	var formats []string
	for f := range strings.SplitSeq(s.TXT["pdl"], ",") {
		if f = strings.TrimSpace(f); f != "" {
			formats = append(formats, f)
		}
	}
	return Printer{
		Name:        s.Instance,
		Description: s.TXT["ty"],
		Location:    s.TXT["note"],
		Caps: Capabilities{
			Formats: formats,
			Color:   strings.EqualFold(s.TXT["color"], "T"),
			Duplex:  strings.EqualFold(s.TXT["duplex"], "T"),
		},
	}
}

// uri returns the URI of the named printer, searching the network again
// if the name is not known from the last search.
func (p *ippEverywhere) uri(ctx context.Context, name string) (string, error) {
	switch {
	case name == "":
		return "", fmt.Errorf("%w: network printers have no default printer", ErrNoPrinter)
	case isPrinterURI(name):
		return name, nil
	}
	p.mu.Lock()
	uri, ok := p.uris[name]
	p.mu.Unlock()
	if ok {
		return uri, nil
	}
	if _, err := p.Printers(ctx); err != nil {
		return "", err
	}
	p.mu.Lock()
	uri, ok = p.uris[name]
	p.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("%w: no network printer %q", ErrPrinterNotFound, name)
	}
	return uri, nil
}

func (p *ippEverywhere) Capabilities(ctx context.Context, printer string) (Capabilities, error) {
	uri, err := p.uri(ctx, printer)
	if err != nil {
		return Capabilities{}, err
	}
	return networkIPP.capabilities(ctx, uri)
}

func (p *ippEverywhere) Print(ctx context.Context, doc Document, s Settings) (*Job, error) {
	uri, err := p.uri(ctx, s.Printer)
	if err != nil {
		return nil, err
	}
	attrs, err := printerAttributes(ctx, uri, s.Credentials, slices.Concat(printerAttrs, rasterAttrs))
	if err != nil {
		return nil, err
	}
	src, err := doc.open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	name := s.Printer
	s.Printer = uri
	formats := capsFromIPP(attrs).Formats
	if len(formats) == 0 || slices.Contains(formats, ipp.DefaultDocumentFormat) {
		return networkIPP.print(ctx, src, doc, s)
	}
	if p.opts.Rasterizer == nil {
		return nil, fmt.Errorf("%w: printer %q accepts no PDF (%s); a Rasterizer (module github.com/timzifer/goprint/raster) renders for it",
			ErrUnsupported, name, strings.Join(formats, ", "))
	}
	f, warnings, err := chooseRaster(attrs, s)
	if err != nil {
		return nil, fmt.Errorf("printer %q: %w", name, err)
	}
	if s.Strict && len(warnings) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	defer pr.Close() // ends the rasterizer if the job is not sent
	go func() { pw.CloseWithError(p.opts.Rasterizer.Rasterize(ctx, pw, data, f)) }()
	return networkIPP.printFormat(ctx, pr, doc, rasterSettings(s, f), f.Type, warnings)
}

// printerAttributes asks the printer at uri for its attributes.
func printerAttributes(ctx context.Context, uri string, creds *Credentials, names []string) (ipp.Attributes, error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	c, err := networkIPP.client(uri, creds)
	if err != nil {
		return nil, err
	}
	p, err := c.GetPrinterAttributes(ctx, uri, names...)
	if err != nil {
		return nil, ippError(err, uri)
	}
	return p.Attrs, nil
}
