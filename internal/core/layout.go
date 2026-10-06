package core

import "math"

// Scaling modes for Layout, following IPP print-scaling.
const (
	ScaleAuto = iota // shrink to fit if too large, else unscaled
	ScaleFit         // scale up or down to fit, keep aspect
	ScaleFill        // scale to cover the paper, keep aspect, crop
	ScaleNone        // unscaled
)

// Layout places a page of pw×ph on paper of qw×qh (same units) and returns
// the scale and the offset of the scaled page's top-left corner. The page
// is centered.
func Layout(pw, ph, qw, qh float64, mode int) (scale, dx, dy float64) {
	fit := math.Min(qw/pw, qh/ph)
	switch mode {
	case ScaleFit:
		scale = fit
	case ScaleFill:
		scale = math.Max(qw/pw, qh/ph)
	case ScaleNone:
		scale = 1
	default:
		scale = math.Min(1, fit)
	}
	return scale, (qw - pw*scale) / 2, (qh - ph*scale) / 2
}
