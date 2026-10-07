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

func FuzzLayout(f *testing.F) {
	f.Add(595.0, 842.0, 842.0, 595.0, 0)
	f.Add(1.0, 1.0, 1000.0, 1.0, 1)
	f.Fuzz(func(t *testing.T, pw, ph, qw, qh float64, mode int) {
		for _, v := range []float64{pw, ph, qw, qh} {
			if !(v > 0.01 && v < 1e6) {
				return
			}
		}
		mode = ((mode % 4) + 4) % 4
		s, dx, dy := Layout(pw, ph, qw, qh, mode)
		if !(s > 0) || math.IsInf(s, 0) || math.IsNaN(dx) || math.IsNaN(dy) {
			t.Fatalf("Layout(%v,%v,%v,%v,%d) = %v,%v,%v", pw, ph, qw, qh, mode, s, dx, dy)
		}
		const eps = 1e-6
		if mode == ScaleFit || mode == ScaleAuto {
			if pw*s > qw*(1+eps) || ph*s > qh*(1+eps) {
				t.Fatalf("mode %d: scaled page %vx%v exceeds paper %vx%v", mode, pw*s, ph*s, qw, qh)
			}
		}
		if mode == ScaleFill && (pw*s < qw*(1-eps) || ph*s < qh*(1-eps)) {
			t.Fatalf("fill leaves paper uncovered")
		}
	})
}
