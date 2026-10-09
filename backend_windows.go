//go:build windows

package goprint

import (
	"context"
	"fmt"
	"io"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/core"
	"github.com/timzifer/goprint/internal/winprint"
	"github.com/timzifer/goprint/ipp"
)

var platform backend = windowsBackend{}

type windowsBackend struct{}

func (windowsBackend) printers(context.Context) ([]Printer, error) {
	ps, err := winprint.Printers()
	if err != nil {
		return nil, err
	}
	out := make([]Printer, 0, len(ps))
	for _, p := range ps {
		out = append(out, Printer{Name: p.Name, Description: p.Comment, Location: p.Location, Default: p.Default, ToFile: winToFile(p)})
	}
	return out, nil
}

// winToFile reports printers that write files: by name, or by a port
// that prompts for a file name ("PORTPROMPT:", "FILE:") or is a local
// file ("C:\out\job.prn"). Ports of shared and network printers
// (`\\server\queue`, "http://...") are not files.
func winToFile(p winprint.PrinterInfo) bool {
	port := strings.ToUpper(strings.TrimSpace(p.Port))
	drivePath := len(port) > 2 && port[0] >= 'A' && port[0] <= 'Z' && port[1] == ':' && port[2] == '\\'
	return fileOutputName(p.Name) || port == "PORTPROMPT:" || port == "FILE:" || drivePath
}

// fileOutputPrinter looks the printer up and reports whether it writes
// files; unknown printers are judged by their name.
func fileOutputPrinter(name string) bool {
	ps, err := winprint.Printers()
	if err == nil {
		for _, p := range ps {
			if strings.EqualFold(p.Name, name) {
				return winToFile(p)
			}
		}
	}
	return fileOutputName(name)
}

// ippDirect handles printers given as ipp:// or ipps:// URI (IPP
// Everywhere network printers) on Windows.
var ippDirect = ippBackend{newClient: func(...ipp.Option) (*ipp.Client, error) {
	return nil, fmt.Errorf("%w: no CUPS server on windows", ErrUnsupported)
}}

func (windowsBackend) capabilities(ctx context.Context, printer string) (Capabilities, error) {
	if isPrinterURI(printer) {
		return ippDirect.capabilities(ctx, printer)
	}
	c, err := winprint.Capabilities(printer)
	if err != nil {
		return Capabilities{}, err
	}
	caps := capsFromWin(c)
	if printer == "" {
		if printer, err = winprint.DefaultPrinter(); err != nil {
			return caps, nil
		}
	}
	// Qualities are optional: drivers without PrintCapabilities just
	// report none.
	if qs, err := winprint.Qualities(ctx, printer); err == nil {
		for _, q := range qs {
			if v := qualityFromDMRes(q); v != QualityDefault {
				caps.Qualities = append(caps.Qualities, v)
			}
		}
	}
	return caps, nil
}

func (windowsBackend) print(ctx context.Context, src io.Reader, doc Document, s Settings) (*Job, error) {
	if isPrinterURI(s.Printer) {
		return ippDirect.print(ctx, src, doc, s)
	}
	js, warnings := toJobSettings(s)
	dm, dmWarnings := baseDevMode(s)
	warnings = append(warnings, dmWarnings...)
	if len(doc.Attributes) > 0 {
		warnings = append(warnings, attributesWarning("the Windows spooler does not pass them on"))
	}
	if s.Strict && len(warnings) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	j, err := winprint.Print(ctx, src, winprint.Options{
		Printer:     s.Printer,
		Title:       doc.Title,
		OutputFile:  s.Vendor[VendorOutputFile],
		PageRanges:  corePageRanges(s.PageRanges),
		Settings:    js,
		BaseDevMode: dm,
		Strict:      s.Strict,
	})
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, fromWinWarnings(j.Warnings())...)
	if j.PagesClipped() {
		warnings = append(warnings, Warning{"PageRanges", "ranges exceed the document's page count"})
	}
	return &Job{b: windowsJob{j}, warnings: warnings}, nil
}

func (windowsBackend) properties(ctx context.Context, s Settings, owner uintptr) (Settings, error) {
	if isPrinterURI(s.Printer) {
		return Settings{}, fmt.Errorf("%w: IPP printers have no driver dialog", ErrUnsupported)
	}
	js, warnings := toJobSettings(s)
	dm, dmWarnings := baseDevMode(s)
	warnings = append(warnings, dmWarnings...)
	if s.Strict && len(warnings) > 0 {
		return Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	res, err := winprint.PropertiesDialog(ctx, windows.HWND(owner), s.Printer, dm, js)
	if err != nil {
		return Settings{}, err
	}
	if s.Strict && len(res.Warnings) > 0 {
		return Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, fromWinWarnings(res.Warnings)[0])
	}
	chosen := withDevMode(fromJobSettings(res.Chosen, s), res.DevMode)
	chosen.Printer = res.Printer
	return chosen, nil
}

func corePageRanges(rs []PageRange) []core.PageRange {
	var out []core.PageRange
	for _, r := range rs {
		out = append(out, core.PageRange{From: r.From, To: r.To})
	}
	return out
}

type windowsJob struct{ j *winprint.Job }

func (w windowsJob) id() string { return w.j.ID() }

func (w windowsJob) state(ctx context.Context) (JobState, error) {
	s, err := w.j.State(ctx)
	if err != nil {
		return JobPending, err
	}
	switch s {
	case winprint.StateProcessing:
		return JobProcessing, nil
	case winprint.StateCompleted:
		return JobCompleted, nil
	case winprint.StateCanceled:
		return JobCanceled, nil
	case winprint.StateAborted:
		return JobAborted, nil
	}
	return JobPending, nil
}

func (w windowsJob) wait(ctx context.Context) error { return w.j.Wait(ctx) }

func (w windowsJob) cancel(ctx context.Context) error { return w.j.Cancel(ctx) }
