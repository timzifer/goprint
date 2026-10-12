package goprint

// Settings mapping for the macOS print panel. This file is plain Go and
// builds everywhere so that the mapping is unit-tested on every platform;
// only dialog_darwin.go uses it at run time.

import (
	"math"
	"strings"

	"github.com/timzifer/goprint/internal/macprint"
)

// micrometersPerPoint converts PDF/AppKit points (1/72 inch) to µm.
const micrometersPerPoint = 25400.0 / 72

// toMacOptions maps settings onto the NSPrintInfo/PMPrintSettings presets
// and reports what cannot be expressed.
func toMacOptions(s Settings, title string) (macprint.Options, []Warning) {
	o := macprint.Options{Title: title, Copies: s.Copies, Extra: map[string]string{}}
	var w []Warning
	warn := func(setting, msg string) { w = append(w, Warning{setting, msg}) }

	if isPrinterURI(s.Printer) {
		warn("Printer", "the macOS print panel cannot preselect an IPP URI; use a queue name")
	} else {
		o.Printer = s.Printer
	}
	if s.Collate != nil {
		o.Collate = macprint.No
		if *s.Collate {
			o.Collate = macprint.Yes
		}
	}
	switch len(s.PageRanges) {
	case 0:
	default:
		warn("PageRanges", "the macOS print panel takes a single page range; using the first")
		fallthrough
	case 1:
		r := s.PageRanges[0]
		if r.From > 1 || r.To != 0 {
			o.FirstPage, o.LastPage = r.From, r.To
		}
	}
	if s.Media != (Media{}) {
		m := s.Media
		if m.Width == 0 || m.Height == 0 {
			m, _ = ParseMedia(m.Name) // validated before
		}
		o.PaperWidth = float64(m.Width) / micrometersPerPoint
		o.PaperHeight = float64(m.Height) / micrometersPerPoint
	}
	switch s.Orientation {
	case Portrait:
		o.Orientation, o.SetOrientation = macprint.OrientationPortrait, true
	case Landscape:
		o.Orientation, o.SetOrientation = macprint.OrientationLandscape, true
	case ReversePortrait, ReverseLandscape:
		warn("Orientation", "reverse orientations cannot be preset on macOS; using the non-reversed one")
		o.Orientation, o.SetOrientation = macprint.OrientationPortrait, true
		if s.Orientation == ReverseLandscape {
			o.Orientation = macprint.OrientationLandscape
		}
	}
	// PDFKit turns pages to fit the paper unless the caller asked for an
	// orientation explicitly.
	o.AutoRotate = !o.SetOrientation
	switch s.Duplex {
	case DuplexNone:
		o.Duplex = macprint.DuplexNone
	case DuplexLongEdge:
		o.Duplex = macprint.DuplexLongEdge
	case DuplexShortEdge:
		o.Duplex = macprint.DuplexShortEdge
	}
	switch s.Color {
	case Color:
		o.Color = macprint.Yes
	case Monochrome:
		o.Color = macprint.No
	}
	switch s.Scaling {
	case ScalingDefault:
		o.Scaling = macprint.ScaleDownToFit
	case ScalingFit:
		o.Scaling = macprint.ScaleToFit
	case ScalingNone:
		o.Scaling = macprint.ScaleNone
	case ScalingFill:
		warn("Scaling", "PDFKit cannot scale to fill; scaling to fit")
		o.Scaling = macprint.ScaleToFit
	}
	// The macOS print system hands print settings it does not know to CUPS
	// as job options, so IPP attribute names reach the queue.
	switch s.Quality {
	case QualityDraft:
		o.Extra["print-quality"] = "3"
	case QualityNormal:
		o.Extra["print-quality"] = "4"
	case QualityHigh:
		o.Extra["print-quality"] = "5"
	}
	if s.Tray != "" {
		o.Extra["media-source"] = s.Tray
	}
	for k, v := range s.Vendor {
		if strings.Contains(k, ":") {
			warn("Vendor["+k+"]", "not an IPP attribute")
			continue
		}
		o.Extra[k] = v
	}
	return o, w
}

// fromMacResult maps what the print panel reports back onto settings,
// starting from the presets so that values the panel does not report stay.
func fromMacResult(r macprint.Result, preset Settings) Settings {
	s := preset
	if r.Printer != "" {
		s.Printer = r.Printer
	}
	if r.Copies > 0 {
		s.Copies = r.Copies
	}
	switch {
	case r.AllPages:
		s.PageRanges = nil
	case r.FirstPage > 0:
		s.PageRanges = []PageRange{{From: r.FirstPage, To: max(r.LastPage, r.FirstPage)}}
	}
	if r.PaperWidth > 0 && r.PaperHeight > 0 {
		// Points are coarse (352.8 µm); round to 0.1 mm so that custom sizes
		// get readable names.
		um := func(pt float64) int { return int(math.Round(pt*micrometersPerPoint/100)) * 100 }
		w, h := um(r.PaperWidth), um(r.PaperHeight)
		s.Media = mediaBySize(w, h, r.PaperName)
	}
	landscape := r.Orientation == macprint.OrientationLandscape
	switch {
	case landscape && preset.Orientation == ReverseLandscape, !landscape && preset.Orientation == ReversePortrait:
		// The panel has no reverse orientations; keep the preset.
	case landscape:
		s.Orientation = Landscape
	default:
		s.Orientation = Portrait
	}
	switch r.Collate {
	case macprint.Yes:
		t := true
		s.Collate = &t
	case macprint.No:
		f := false
		s.Collate = &f
	}
	switch r.Duplex {
	case macprint.DuplexNone:
		s.Duplex = DuplexNone
	case macprint.DuplexLongEdge:
		s.Duplex = DuplexLongEdge
	case macprint.DuplexShortEdge:
		s.Duplex = DuplexShortEdge
	}
	if c, ok := macColor(r.ColorModel, r.PrintColorMode); ok {
		s.Color = c
	}
	return s
}

// macColor interprets the "ColorModel" PPD option or, failing that, the
// "print-color-mode" setting.
func macColor(colorModel, printColorMode string) (ColorMode, bool) {
	switch strings.ToLower(colorModel) {
	case "gray", "grayscale", "black", "kgray", "monochrome":
		return Monochrome, true
	case "rgb", "cmyk", "cmy", "rgbw", "color", "colour":
		return Color, true
	}
	switch strings.ToLower(printColorMode) {
	case "monochrome", "auto-monochrome", "process-monochrome", "bi-level", "process-bi-level":
		return Monochrome, true
	case "color":
		return Color, true
	}
	return ColorAuto, false
}

// spooledJob is a CUPS job as listed by Get-Jobs.
type spooledJob struct {
	ID      int
	Name    string
	Printer string // queue name from job-printer-uri
}

// pickSpooledJob finds the job the print panel just created among jobs:
// the newest one above baseline (the newest job id before the dialog)
// whose name is title, or the only new job if no name matches.
func pickSpooledJob(jobs []spooledJob, baseline int, title string) (spooledJob, bool) {
	var named, newest spooledJob
	n := 0
	for _, j := range jobs {
		if j.ID <= baseline {
			continue
		}
		n++
		if j.ID > newest.ID {
			newest = j
		}
		if j.Name == title && j.ID > named.ID {
			named = j
		}
	}
	switch {
	case named.ID > 0:
		return named, true
	case n == 1:
		return newest, true
	}
	return spooledJob{}, false
}

// needsPDFKitLayout reports whether a job with s is printed through PDFKit
// instead of CUPS on macOS: it asks for an orientation or scaling, which
// the macOS PDF filter does not lay out, and has nothing PDFKit cannot
// express (strict fidelity, IPP URIs, credentials, several page ranges).
func needsPDFKitLayout(s Settings) bool {
	if s.Orientation == OrientationDefault && s.Scaling == ScalingDefault {
		return false
	}
	return !s.Strict && s.Credentials == nil && !isPrinterURI(s.Printer) && len(s.PageRanges) <= 1
}
