package goprint

// The mapping between Settings and UIKit's UIPrintInfo for the iOS
// backend (backend_ios.go). It builds everywhere so that its tests run on
// every platform.

// iosPrintInfo are the UIPrintInfo values of a job.
type iosPrintInfo struct {
	jobName     string
	printerID   string
	duplex      int // UIPrintInfoDuplex: 0 none, 1 long edge, 2 short edge
	orientation int // UIPrintInfoOrientation: 0 portrait, 1 landscape
	outputType  int // UIPrintInfoOutputType: 0 general, 1 photo, 2 grayscale, 3 photo grayscale
}

// toIOSPrintInfo maps settings onto UIPrintInfo. What it has no place
// for is reported as warnings.
func toIOSPrintInfo(s Settings, title string) (iosPrintInfo, []Warning) {
	info := iosPrintInfo{jobName: title, printerID: s.Printer, duplex: -1}
	var w []Warning
	warn := func(setting, msg string) { w = append(w, Warning{setting, msg}) }
	switch s.Duplex {
	case DuplexNone:
		info.duplex = 0
	case DuplexLongEdge:
		info.duplex = 1
	case DuplexShortEdge:
		info.duplex = 2
	}
	switch s.Orientation {
	case Landscape:
		info.orientation = 1
	case ReversePortrait, ReverseLandscape:
		warn("Orientation", "iOS prints portrait or landscape only")
		if s.Orientation == ReverseLandscape {
			info.orientation = 1
		}
	}
	switch s.Color {
	case Monochrome:
		info.outputType = 2
		if s.Quality == QualityHigh {
			info.outputType = 3
		}
	default:
		if s.Quality == QualityHigh {
			info.outputType = 1
		}
	}
	if s.Quality == QualityDraft {
		warn("Quality", "iOS has no draft quality")
	}
	if s.Copies > 1 {
		warn("Copies", "iOS takes the number of copies only in its print sheet")
	}
	if len(s.PageRanges) > 0 {
		warn("PageRanges", "iOS takes page ranges only in its print sheet")
	}
	if s.Media.Name != "" || s.Media.Width > 0 {
		warn("Media", "iOS chooses the paper itself")
	}
	if s.Tray != "" {
		warn("Tray", "iOS chooses the paper source itself")
	}
	if s.Collate != nil {
		warn("Collate", "iOS takes collation only in its print sheet")
	}
	if s.Scaling != ScalingDefault {
		warn("Scaling", "iOS scales to the paper itself")
	}
	for k := range s.Vendor {
		warn("Vendor["+k+"]", "not supported on iOS")
	}
	return info, w
}

// fromIOSPrintInfo returns s with what the user chose in the sheet.
func fromIOSPrintInfo(s Settings, info iosPrintInfo) Settings {
	if info.printerID != "" {
		s.Printer = info.printerID
	}
	switch info.duplex {
	case 0:
		s.Duplex = DuplexNone
	case 1:
		s.Duplex = DuplexLongEdge
	case 2:
		s.Duplex = DuplexShortEdge
	}
	if info.orientation == 1 {
		s.Orientation = Landscape
	} else if s.Orientation == Landscape {
		s.Orientation = Portrait
	}
	switch info.outputType {
	case 2, 3:
		s.Color = Monochrome
	default:
		if s.Color == Monochrome {
			s.Color = ColorAuto
		}
	}
	return s
}
