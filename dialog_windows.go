//go:build windows

package goprint

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/winprint"
)

// Windows.Graphics.Printing enum values used by the modern dialog.
const (
	wrtOrientationPortrait         = 3
	wrtOrientationPortraitFlipped  = 4
	wrtOrientationLandscape        = 5
	wrtOrientationLandscapeFlipped = 6

	wrtDuplexOneSided  = 3
	wrtDuplexShortEdge = 4
	wrtDuplexLongEdge  = 5
	wrtColorColor      = 3
	wrtColorGrayscale  = 4
	wrtColorMonochrome = 5
	wrtCollated        = 3
	wrtUncollated      = 4
	wrtQualityDraft    = 4
	wrtQualityHigh     = 6
	wrtQualityNormal   = 7
)

// wrtMediaSizes maps PWG media names to Windows.Graphics.Printing.PrintMediaSize.
var wrtMediaSizes = map[string]int32{
	"iso_a3_297x420mm":         9,
	"iso_a4_210x297mm":         12,
	"iso_a5_148x210mm":         15,
	"iso_a6_105x148mm":         18,
	"jis_b4_257x364mm":         81,
	"jis_b5_182x257mm":         83,
	"na_executive_7.25x10.5in": 108,
	"na_legal_8.5x14in":        111,
	"na_letter_8.5x11in":       113,
	"na_ledger_11x17in":        130,
}

// toTaskOptions maps settings onto the modern dialog's options and reports
// what it cannot express.
func toTaskOptions(s Settings) (winprint.TaskOptions, []Warning) {
	var o winprint.TaskOptions
	var w []Warning
	warn := func(name, msg string) { w = append(w, Warning{name, msg}) }

	if s.Copies > 0 {
		o.Copies = uint32(s.Copies)
	}
	if s.Media != (Media{}) {
		if v, ok := wrtMediaSizes[s.Media.Name]; ok {
			o.MediaSize = v
		} else {
			warn("Media", fmt.Sprintf("%s cannot be preset in the windows print dialog", s.Media))
		}
	}
	switch s.Orientation {
	case Portrait:
		o.Orientation = wrtOrientationPortrait
	case ReversePortrait:
		o.Orientation = wrtOrientationPortraitFlipped
	case Landscape:
		o.Orientation = wrtOrientationLandscape
	case ReverseLandscape:
		o.Orientation = wrtOrientationLandscapeFlipped
	}
	switch s.Duplex {
	case DuplexNone:
		o.Duplex = wrtDuplexOneSided
	case DuplexLongEdge:
		o.Duplex = wrtDuplexLongEdge
	case DuplexShortEdge:
		o.Duplex = wrtDuplexShortEdge
	}
	switch s.Color {
	case Color:
		o.Color = wrtColorColor
	case Monochrome:
		o.Color = wrtColorMonochrome
	}
	if s.Collate != nil {
		o.Collation = wrtUncollated
		if *s.Collate {
			o.Collation = wrtCollated
		}
	}
	switch s.Quality {
	case QualityDraft:
		o.Quality = wrtQualityDraft
	case QualityNormal:
		o.Quality = wrtQualityNormal
	case QualityHigh:
		o.Quality = wrtQualityHigh
	}
	if s.Printer != "" {
		warn("Printer", "the modern windows print dialog cannot preselect a printer")
	}
	if len(s.PageRanges) > 0 {
		warn("PageRanges", "the modern windows print dialog cannot preset page ranges; the user picks them")
	}
	if s.Scaling != ScalingDefault {
		warn("Scaling", "not yet supported on windows")
	}
	if s.Tray != "" {
		warn("Tray", "not yet supported on windows")
	}
	for k := range s.Vendor {
		warn("Vendor["+k+"]", "not supported by the windows print dialog")
	}
	return o, w
}

// fromTaskOptions maps the confirmed dialog options back onto settings,
// starting from the presets so that values the dialog does not report stay.
func fromTaskOptions(o winprint.TaskOptions, preset Settings) Settings {
	s := preset
	s.Printer = ""
	if o.Copies > 0 {
		s.Copies = int(o.Copies)
	}
	for name, v := range wrtMediaSizes {
		if v == o.MediaSize {
			if m, err := ParseMedia(name); err == nil {
				s.Media = m
			}
		}
	}
	switch o.Orientation {
	case wrtOrientationPortrait:
		s.Orientation = Portrait
	case wrtOrientationPortraitFlipped:
		s.Orientation = ReversePortrait
	case wrtOrientationLandscape:
		s.Orientation = Landscape
	case wrtOrientationLandscapeFlipped:
		s.Orientation = ReverseLandscape
	}
	switch o.Duplex {
	case wrtDuplexOneSided:
		s.Duplex = DuplexNone
	case wrtDuplexLongEdge:
		s.Duplex = DuplexLongEdge
	case wrtDuplexShortEdge:
		s.Duplex = DuplexShortEdge
	}
	switch o.Color {
	case wrtColorColor:
		s.Color = Color
	case wrtColorGrayscale, wrtColorMonochrome:
		s.Color = Monochrome
	}
	switch o.Collation {
	case wrtCollated:
		t := true
		s.Collate = &t
	case wrtUncollated:
		f := false
		s.Collate = &f
	}
	switch o.Quality {
	case wrtQualityDraft:
		s.Quality = QualityDraft
	case wrtQualityNormal:
		s.Quality = QualityNormal
	case wrtQualityHigh:
		s.Quality = QualityHigh
	}
	return s
}

func (windowsBackend) dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	classic := opts.Style == StyleClassic || (opts.Style == StyleAuto && !opts.PrintNow)
	if opts.RequirePrinter && opts.Settings.Printer != "" {
		if winprint.LegacyDialogRedirected() {
			// Windows 11 shows PrintDlgEx as its modern dialog, which ignores
			// the printer preselection.
			return nil, Settings{}, fmt.Errorf("%w: this Windows print dialog cannot preselect a printer (RequirePrinter)", ErrUnsupported)
		}
		classic = true
	}
	if opts.Style == StyleModern {
		classic = false
	}
	src, err := doc.open()
	if err != nil {
		return nil, Settings{}, err
	}
	defer src.Close()
	title := doc.Title
	if title == "" {
		title = "Document"
	}
	if classic {
		return classicDialog(ctx, src, title, opts)
	}

	presets, warnings := toTaskOptions(opts.Settings)
	if opts.Settings.Strict && len(warnings) > 0 {
		return nil, Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	res, err := winprint.Dialog(ctx, src, winprint.DialogOptions{
		Owner:    windows.HWND(opts.Owner),
		Title:    title,
		Presets:  presets,
		PrintNow: opts.PrintNow,
	})
	if err != nil {
		return nil, Settings{}, err
	}
	chosen := fromTaskOptions(res.Chosen, opts.Settings)
	if res.Job == nil {
		return nil, chosen, nil
	}
	chosen.Printer = res.Job.Printer()
	return &Job{b: windowsJob{res.Job}, warnings: warnings}, chosen, nil
}

// classicDialog runs PrintDlgExW: full DEVMODE presets, page ranges, no
// preview, and no job unless PrintNow.
func classicDialog(ctx context.Context, src io.Reader, title string, opts DialogOptions) (*Job, Settings, error) {
	js, warnings := toJobSettings(opts.Settings)
	dm, dmWarnings := baseDevMode(opts.Settings)
	warnings = append(warnings, dmWarnings...)
	if opts.Settings.Printer != "" && winprint.LegacyDialogRedirected() {
		warnings = append(warnings, Warning{"Printer", "this Windows print dialog cannot preselect a printer"})
	}
	if opts.Settings.Strict && len(warnings) > 0 {
		return nil, Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	res, err := winprint.ClassicDialog(ctx, src, winprint.ClassicOptions{
		Owner:       windows.HWND(opts.Owner),
		Title:       title,
		Printer:     opts.Settings.Printer,
		Settings:    js,
		BaseDevMode: dm,
		PageRanges:  corePageRanges(opts.Settings.PageRanges),
		PrintNow:    opts.PrintNow,
	})
	if err != nil {
		return nil, Settings{}, err
	}
	warnings = append(warnings, fromWinWarnings(res.Warnings)...)
	chosen := withDevMode(fromJobSettings(res.Chosen, opts.Settings), res.DevMode)
	chosen.Printer = res.Printer
	chosen.PageRanges = nil
	for _, r := range res.PageRanges {
		chosen.PageRanges = append(chosen.PageRanges, PageRange{From: r.From, To: r.To})
	}
	if res.Job == nil {
		return nil, chosen, nil
	}
	return &Job{b: windowsJob{res.Job}, warnings: warnings}, chosen, nil
}
