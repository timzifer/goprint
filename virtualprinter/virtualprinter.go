// Package virtualprinter simulates printers for goprint. A [Provider] is
// a [goprint.Provider] that keeps every job in memory as PDF instead of
// printing it, so programs and tests can print without hardware:
//
//	vp := virtualprinter.New("virtual", virtualprinter.Office("Office"), virtualprinter.Label("Label-62"))
//	goprint.Default = goprint.NewClient(goprint.System(), vp)
//	job, err := goprint.Print(ctx, doc, goprint.Settings{Provider: "virtual", Printer: "Label-62"})
//	// vp.Jobs()[0].PDF holds the document.
//
// Settings a printer cannot honor (by its [goprint.Capabilities]) become
// warnings, or errors under Settings.Strict, as with real printers.
// Failures are scripted: [Provider.FailNext], [Provider.SetOffline],
// [Provider.HoldJobs] and [Printer.Latency].
//
// [Provider.Listen] (or [Provider.Handler]) serves the printers over IPP,
// so that other processes and devices print to them too: goprint by
// printer URI, CUPS, or any IPP client. [Server.Advertise] announces them
// on the local network with DNS-SD.
package virtualprinter

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/core"
)

// Printer is a simulated printer.
type Printer struct {
	Name        string
	Description string
	Location    string
	// Default marks the provider's default printer. Without one, the
	// first printer is the default.
	Default bool
	// ToFile reports the printer as one that writes files.
	ToFile bool
	Caps   goprint.Capabilities
	// Latency delays every Print to this printer, e.g. to show progress
	// in a UI. A canceled context ends the wait.
	Latency time.Duration
}

// Provider is a set of simulated printers and the jobs printed to them.
// It is safe for concurrent use.
type Provider struct {
	name string

	mu       sync.Mutex
	printers []Printer
	offline  map[string]bool
	failNext []error
	hold     bool
	jobs     []*Job
	nextID   int
}

var _ goprint.Provider = (*Provider)(nil)

// New returns a provider named name with the given printers. It panics if
// name is empty, which is reserved for goprint's System provider.
func New(name string, printers ...Printer) *Provider {
	if name == "" {
		panic("virtualprinter: empty provider name")
	}
	return &Provider{name: name, printers: slices.Clone(printers), offline: map[string]bool{}, nextID: 1}
}

// Name implements [goprint.Provider].
func (p *Provider) Name() string { return p.name }

// AddPrinter adds pr, or replaces the printer with the same name.
func (p *Provider) AddPrinter(pr Printer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := p.index(pr.Name); i >= 0 {
		p.printers[i] = pr
		return
	}
	p.printers = append(p.printers, pr)
}

// RemovePrinter removes the named printer and reports whether it existed.
// Its jobs stay in [Provider.Jobs].
func (p *Provider) RemovePrinter(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.index(name)
	if i < 0 {
		return false
	}
	p.printers = slices.Delete(p.printers, i, i+1)
	return true
}

// SetOffline makes Print to the named printer fail with [goprint.ErrBusy]
// until it is set online again. The printer is still listed.
func (p *Provider) SetOffline(printer string, offline bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if offline {
		p.offline[printer] = true
	} else {
		delete(p.offline, printer)
	}
}

// FailNext makes the next Print fail with err, without creating a job.
// Several calls queue up, one error per Print.
func (p *Provider) FailNext(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext = append(p.failNext, err)
}

// HoldJobs controls the state of new jobs. Held jobs stay pending until
// [Job.SetState] moves them on; otherwise jobs complete at once.
func (p *Provider) HoldJobs(hold bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hold = hold
}

// Jobs returns the printed jobs, oldest first.
func (p *Provider) Jobs() []*Job {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.jobs)
}

// Reset removes all jobs and scripted failures and sets every printer
// online. Printers stay.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jobs, p.failNext, p.hold, p.nextID = nil, nil, false, 1
	clear(p.offline)
}

// Printers implements [goprint.Provider].
func (p *Provider) Printers(context.Context) ([]goprint.Printer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	def := p.defaultIndex()
	out := make([]goprint.Printer, len(p.printers))
	for i, pr := range p.printers {
		out[i] = goprint.Printer{
			Provider:    p.name,
			Name:        pr.Name,
			Description: pr.Description,
			Location:    pr.Location,
			Default:     i == def,
			ToFile:      pr.ToFile,
			Caps:        pr.Caps,
		}
	}
	return out, nil
}

// Capabilities implements [goprint.Provider].
func (p *Provider) Capabilities(_ context.Context, printer string) (goprint.Capabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, err := p.lookup(printer)
	if err != nil {
		return goprint.Capabilities{}, err
	}
	return pr.Caps, nil
}

// Print implements [goprint.Provider]. It keeps doc as PDF in a new [Job].
func (p *Provider) Print(ctx context.Context, doc goprint.Document, s goprint.Settings) (*goprint.Job, error) {
	j, err := p.print(ctx, doc, s, 0)
	if err != nil {
		return nil, err
	}
	return goprint.NewJob(handle{j}, j.Warnings), nil
}

// reserveID returns a job id for a job that is printed later (IPP
// Create-Job, then Send-Document).
func (p *Provider) reserveID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.nextID
	p.nextID++
	return id
}

// print prints doc as Print does and returns the job; id is a reserved
// job id, or 0 for a new one.
func (p *Provider) print(ctx context.Context, doc goprint.Document, s goprint.Settings, id int) (*Job, error) {
	p.mu.Lock()
	pr, err := p.lookup(s.Printer)
	if err == nil && len(p.failNext) > 0 {
		err, p.failNext = p.failNext[0], p.failNext[1:]
	}
	if err == nil && p.offline[pr.Name] {
		err = fmt.Errorf("%w: virtual printer %q is offline", goprint.ErrBusy, pr.Name)
	}
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}

	warnings := check(s, pr.Caps)
	if s.Strict && len(warnings) > 0 {
		return nil, fmt.Errorf("%w: %s", goprint.ErrUnsupported, warnings[0])
	}
	if pr.Latency > 0 {
		t := time.NewTimer(pr.Latency)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
	data, err := pdf(doc)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if id == 0 {
		id = p.nextID
		p.nextID++
	}
	j := &Job{
		ID:         id,
		Printer:    pr.Name,
		Title:      doc.Title,
		Settings:   s,
		Attributes: maps.Clone(doc.Attributes),
		PDF:        data,
		Warnings:   warnings,
		state:      goprint.JobCompleted,
		done:       make(chan struct{}),
	}
	if p.hold {
		j.state = goprint.JobPending
	} else {
		close(j.done)
	}
	p.jobs = append(p.jobs, j)
	return j, nil
}

// job returns the job with the id, or nil.
func (p *Provider) job(id int) *Job {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, j := range p.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// printer returns the named printer and whether it is offline.
func (p *Provider) printer(name string) (pr Printer, offline, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.index(name)
	if i < 0 {
		return Printer{}, false, false
	}
	return p.printers[i], p.offline[name], true
}

// index returns the position of the named printer, or -1.
func (p *Provider) index(name string) int {
	return slices.IndexFunc(p.printers, func(pr Printer) bool { return pr.Name == name })
}

// defaultIndex returns the position of the default printer, or -1.
func (p *Provider) defaultIndex() int {
	if len(p.printers) == 0 {
		return -1
	}
	if i := slices.IndexFunc(p.printers, func(pr Printer) bool { return pr.Default }); i >= 0 {
		return i
	}
	return 0
}

// lookup returns the named printer; "" is the default printer.
func (p *Provider) lookup(name string) (Printer, error) {
	i := p.defaultIndex()
	if name != "" {
		i = p.index(name)
	}
	switch {
	case i >= 0:
		return p.printers[i], nil
	case name == "":
		return Printer{}, fmt.Errorf("%w: virtual provider %q has no printers", goprint.ErrNoPrinter, p.name)
	}
	return Printer{}, fmt.Errorf("%w: virtual printer %q", goprint.ErrPrinterNotFound, name)
}

// pdf returns the document as PDF; images are wrapped one per page.
func pdf(doc goprint.Document) ([]byte, error) {
	if doc.PDF == nil {
		return core.ImagesToPDF(doc.Images, doc.DPI)
	}
	r, err := doc.PDF()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// check reports the settings caps cannot honor, in the words of the
// platform backends.
func check(s goprint.Settings, caps goprint.Capabilities) []goprint.Warning {
	var w []goprint.Warning
	warn := func(setting, msg string) { w = append(w, goprint.Warning{Setting: setting, Message: msg}) }
	if m := s.Media; (m.Name != "" || m.Width > 0) && len(caps.Media) > 0 &&
		!slices.ContainsFunc(caps.Media, func(c goprint.Media) bool {
			return (m.Name != "" && c.Name == m.Name) || (m.Name == "" && c.Width == m.Width && c.Height == m.Height)
		}) {
		warn("Media", "paper size not offered by the printer")
	}
	if (s.Duplex == goprint.DuplexLongEdge || s.Duplex == goprint.DuplexShortEdge) && !caps.Duplex {
		warn("Duplex", "printer cannot print two-sided")
	}
	if s.Color == goprint.Color && !caps.Color {
		warn("Color", "printer cannot print in color")
	}
	if s.Tray != "" && !slices.Contains(caps.Trays, s.Tray) {
		warn("Tray", fmt.Sprintf("printer has no paper source %q", s.Tray))
	}
	if s.Quality != goprint.QualityDefault && len(caps.Qualities) > 0 && !slices.Contains(caps.Qualities, s.Quality) {
		warn("Quality", "printer does not offer this quality")
	}
	keys := make([]string, 0, len(s.Vendor))
	for k := range s.Vendor {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		warn("Vendor["+k+"]", "not supported by virtual printers")
	}
	return w
}
