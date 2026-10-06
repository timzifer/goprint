//go:build windows

package goprint

import (
	"github.com/timzifer/goprint/internal/core"
	"github.com/timzifer/goprint/internal/winprint"
)

// DEVMODE values (wingdi.h).
const (
	dmOrientPortrait  = 1
	dmOrientLandscape = 2
	dmDupSimplex      = 1
	dmDupLongEdge     = 2
	dmDupShortEdge    = 3
	dmColorMono       = 1
	dmColorColor      = 2
	dmResDraft        = -1
	dmResMedium       = -3
	dmResHigh         = -4
)

// toJobSettings maps settings onto what the Windows backend applies through
// DEVMODE/PrintTicket and reports what it cannot express.
func toJobSettings(s Settings) (winprint.JobSettings, []Warning) {
	var j winprint.JobSettings
	var w []Warning
	warn := func(setting, msg string) { w = append(w, Warning{setting, msg}) }

	j.Copies = s.Copies
	j.Collate = s.Collate
	if s.Media != (Media{}) {
		m := s.Media
		if m.Width == 0 || m.Height == 0 {
			m, _ = ParseMedia(m.Name) // validated before
		}
		j.PaperWidth, j.PaperHeight = m.Width, m.Height
	}
	switch s.Orientation {
	case Portrait:
		j.Orientation = dmOrientPortrait
	case Landscape:
		j.Orientation = dmOrientLandscape
	case ReversePortrait, ReverseLandscape:
		warn("Orientation", "reverse orientations are not supported on windows; using the non-reversed one")
		j.Orientation = dmOrientPortrait
		if s.Orientation == ReverseLandscape {
			j.Orientation = dmOrientLandscape
		}
	}
	switch s.Duplex {
	case DuplexNone:
		j.Duplex = dmDupSimplex
	case DuplexLongEdge:
		j.Duplex = dmDupLongEdge
	case DuplexShortEdge:
		j.Duplex = dmDupShortEdge
	}
	switch s.Color {
	case Color:
		j.Color = dmColorColor
	case Monochrome:
		j.Color = dmColorMono
	}
	switch s.Quality {
	case QualityDraft:
		j.Quality = dmResDraft
	case QualityNormal:
		j.Quality = dmResMedium
	case QualityHigh:
		j.Quality = dmResHigh
	}
	switch s.Scaling {
	case ScalingFit:
		j.Scaling = core.ScaleFit
	case ScalingFill:
		j.Scaling = core.ScaleFill
	case ScalingNone:
		j.Scaling = core.ScaleNone
	}
	j.Tray = s.Tray
	if s.Credentials != nil {
		warn("Credentials", "not used for windows printers")
	}
	for k := range s.Vendor {
		if k != VendorOutputFile {
			warn("Vendor["+k+"]", "not supported on windows")
		}
	}
	return j, w
}

func capsFromWin(c winprint.Caps) Capabilities {
	out := Capabilities{
		Duplex:        c.Duplex,
		Color:         c.Color,
		Formats:       []string{"application/pdf"},
		DialogPreview: true,
	}
	seen := map[string]bool{}
	for _, p := range c.Papers {
		if p.Width <= 0 || p.Height <= 0 {
			continue
		}
		m := mediaBySize(p.Width, p.Height, p.Name)
		if !seen[m.Name] {
			seen[m.Name] = true
			out.Media = append(out.Media, m)
		}
	}
	for _, r := range c.Resolutions {
		if r[0] > 0 && r[1] > 0 {
			out.Resolutions = append(out.Resolutions, Resolution{X: r[0], Y: r[1]})
		}
	}
	return out
}

func fromWinWarnings(ws []winprint.Warning) []Warning {
	var out []Warning
	for _, w := range ws {
		out = append(out, Warning{w.Setting, w.Message})
	}
	return out
}
