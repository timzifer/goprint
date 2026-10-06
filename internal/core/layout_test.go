package core

import (
	"math"
	"testing"
)

func TestLayout(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	tests := []struct {
		name           string
		pw, ph, qw, qh float64
		mode           int
		scale, dx, dy  float64
	}{
		{"auto fits", 50, 100, 100, 200, ScaleAuto, 1, 25, 50},
		{"auto shrinks", 200, 100, 100, 100, ScaleAuto, 0.5, 0, 25},
		{"fit grows", 50, 50, 100, 200, ScaleFit, 2, 0, 50},
		{"fill crops", 100, 50, 100, 100, ScaleFill, 2, -50, 0},
		{"none", 200, 100, 100, 100, ScaleNone, 1, -50, 0},
	}
	for _, tt := range tests {
		s, dx, dy := Layout(tt.pw, tt.ph, tt.qw, tt.qh, tt.mode)
		if !near(s, tt.scale) || !near(dx, tt.dx) || !near(dy, tt.dy) {
			t.Errorf("%s: Layout = %v, %v, %v; want %v, %v, %v", tt.name, s, dx, dy, tt.scale, tt.dx, tt.dy)
		}
	}
}
