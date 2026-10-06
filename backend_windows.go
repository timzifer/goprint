//go:build windows

package goprint

import (
	"context"
	"fmt"

	"github.com/timzifer/goprint/internal/winprint"
)

// VendorOutputFile is a Settings.Vendor key understood on Windows: the
// printer output is written to this file instead of the device (print to
// file). With "Microsoft Print to PDF" this yields a PDF without the save
// dialog.
const VendorOutputFile = "windows:output-file"

var platform backend = windowsBackend{}

type windowsBackend struct{}

func (windowsBackend) printers(context.Context) ([]Printer, error) {
	ps, err := winprint.Printers()
	if err != nil {
		return nil, err
	}
	out := make([]Printer, 0, len(ps))
	for _, p := range ps {
		out = append(out, Printer{Name: p.Name, Description: p.Comment, Location: p.Location, Default: p.Default})
	}
	return out, nil
}

func (windowsBackend) capabilities(context.Context, string) (Capabilities, error) {
	// TODO(phase 3): PrintCapabilities via prntvpt.dll.
	return Capabilities{}, fmt.Errorf("%w: capabilities on windows (not yet implemented)", ErrUnsupported)
}

func (windowsBackend) print(ctx context.Context, doc Document, s Settings) (*Job, error) {
	warnings := windowsUnmapped(s)
	if s.Strict && len(warnings) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	src, err := doc.open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	j, err := winprint.Print(ctx, src, winprint.Options{
		Printer:    s.Printer,
		Title:      doc.Title,
		OutputFile: s.Vendor[VendorOutputFile],
		PageRanges: s.corePageRanges(),
	})
	if err != nil {
		return nil, err
	}
	if j.PagesClipped() {
		warnings = append(warnings, Warning{"PageRanges", "ranges exceed the document's page count"})
	}
	return &Job{b: windowsJob{j}, warnings: warnings}, nil
}

func (windowsBackend) dialog(context.Context, Document, DialogOptions) (*Job, Settings, error) {
	// TODO(phase 1b/3): modern dialog with preview, classic PrintDlgEx.
	return nil, Settings{}, fmt.Errorf("%w: dialogs on windows are not implemented yet", ErrNoDialog)
}

// windowsUnmapped reports settings the Windows backend does not apply yet.
// TODO(phase 3): map them into a PrintTicket.
func windowsUnmapped(s Settings) []Warning {
	var w []Warning
	add := func(cond bool, name string) {
		if cond {
			w = append(w, Warning{name, "not yet supported on windows"})
		}
	}
	add(s.Copies > 1, "Copies")
	add(s.Collate != nil, "Collate")
	add(s.Media != (Media{}), "Media")
	add(s.Orientation != OrientationDefault, "Orientation")
	add(s.Duplex != DuplexDefault, "Duplex")
	add(s.Color != ColorAuto, "Color")
	add(s.Quality != QualityDefault, "Quality")
	add(s.Scaling != ScalingDefault, "Scaling")
	add(s.Tray != "", "Tray")
	add(s.Credentials != nil, "Credentials")
	for k := range s.Vendor {
		add(k != VendorOutputFile, "Vendor["+k+"]")
	}
	return w
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
