package goprint

import "math"

// The mapping between Settings and Android's PrintAttributes for the
// Android backend (dialog_android.go). It builds everywhere so that its
// tests run on every platform.

// Constants of android.print.PrintAttributes.
const (
	androidColorMono   = 1
	androidColorColor  = 2
	androidDuplexNone  = 1
	androidDuplexLong  = 2
	androidDuplexShort = 4
)

// androidAttrs are the presets of the print dialog; 0 is the default.
type androidAttrs struct {
	color, duplex int
	orientation   int // 1 landscape, 2 portrait
}

// toAndroidAttrs maps settings onto PrintAttributes. What the dialog
// cannot be preset with is reported as warnings.
func toAndroidAttrs(s Settings) (androidAttrs, []Warning) {
	var a androidAttrs
	var w []Warning
	warn := func(setting, msg string) { w = append(w, Warning{setting, msg}) }
	switch s.Color {
	case Monochrome:
		a.color = androidColorMono
	case Color:
		a.color = androidColorColor
	}
	switch s.Duplex {
	case DuplexNone:
		a.duplex = androidDuplexNone
	case DuplexLongEdge:
		a.duplex = androidDuplexLong
	case DuplexShortEdge:
		a.duplex = androidDuplexShort
	}
	switch s.Orientation {
	case Landscape:
		a.orientation = 1
	case Portrait:
		a.orientation = 2
	case ReverseLandscape, ReversePortrait:
		warn("Orientation", "Android prints portrait or landscape only")
		a.orientation = map[Orientation]int{ReverseLandscape: 1, ReversePortrait: 2}[s.Orientation]
	}
	if s.Printer != "" {
		warn("Printer", "Android's print dialog cannot preselect a printer")
	}
	if s.Copies > 1 {
		warn("Copies", "Android takes the number of copies only in its print dialog")
	}
	if len(s.PageRanges) > 0 {
		warn("PageRanges", "Android takes page ranges only in its print dialog")
	}
	if s.Media.Name != "" || s.Media.Width > 0 {
		warn("Media", "Android takes the paper size only in its print dialog")
	}
	if s.Tray != "" {
		warn("Tray", "Android chooses the paper source itself")
	}
	if s.Quality != QualityDefault {
		warn("Quality", "Android has no print quality setting")
	}
	if s.Collate != nil {
		warn("Collate", "Android chooses collation itself")
	}
	if s.Scaling != ScalingDefault {
		warn("Scaling", "Android scales to the paper itself")
	}
	for k := range s.Vendor {
		warn("Vendor["+k+"]", "not supported on Android")
	}
	return a, w
}

// androidResult is what PDFAdapter.result reports: {state, copies,
// colorMode, duplexMode, widthMils, heightMils, landscape, n, ranges...}.
type androidResult []int

// Job states of PDFAdapter.
const (
	androidCanceled = iota
	androidFailed
	androidPending
	androidProcessing
	androidCompleted
)

// jobState maps a PDFAdapter state onto a JobState.
func androidJobState(st int) JobState {
	switch st {
	case androidCanceled:
		return JobCanceled
	case androidFailed:
		return JobAborted
	case androidProcessing:
		return JobProcessing
	case androidCompleted:
		return JobCompleted
	}
	return JobPending
}

// settings returns s with what the user chose in the dialog.
func (r androidResult) settings(s Settings) Settings {
	if len(r) < 8 {
		return s
	}
	if r[1] > 0 {
		s.Copies = r[1]
	}
	switch r[2] {
	case androidColorMono:
		s.Color = Monochrome
	case androidColorColor:
		s.Color = Color
	}
	switch r[3] {
	case androidDuplexNone:
		s.Duplex = DuplexNone
	case androidDuplexLong:
		s.Duplex = DuplexLongEdge
	case androidDuplexShort:
		s.Duplex = DuplexShortEdge
	}
	if r[4] > 0 && r[5] > 0 {
		um := func(mils int) int { return int(math.Round(float64(mils) * 25.4)) }
		s.Media = mediaBySize(um(r[4]), um(r[5]), "android")
		if r[6] == 1 {
			s.Orientation = Landscape
		} else {
			s.Orientation = Portrait
		}
	}
	n := r[7]
	s.PageRanges = nil
	for i := 0; i < n && 9+2*i < len(r); i++ {
		s.PageRanges = append(s.PageRanges, PageRange{From: r[8+2*i] + 1, To: r[9+2*i] + 1})
	}
	return s
}
