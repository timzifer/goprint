package core

import (
	"slices"
	"testing"
)

func TestSelectPages(t *testing.T) {
	tests := []struct {
		ranges  []PageRange
		n       int
		want    []int
		clipped bool
	}{
		{nil, 3, []int{0, 1, 2}, false},
		{[]PageRange{{2, 3}}, 5, []int{1, 2}, false},
		{[]PageRange{{4, 0}}, 5, []int{3, 4}, false},
		{[]PageRange{{1, 1}, {3, 3}, {1, 1}}, 3, []int{0, 2, 0}, false},
		{[]PageRange{{2, 9}}, 3, []int{1, 2}, true},
		{[]PageRange{{7, 8}}, 3, nil, true},
	}
	for _, tt := range tests {
		got, clipped := SelectPages(tt.ranges, tt.n)
		if !slices.Equal(got, tt.want) || clipped != tt.clipped {
			t.Errorf("SelectPages(%v, %d) = %v, %v; want %v, %v", tt.ranges, tt.n, got, clipped, tt.want, tt.clipped)
		}
	}
}

func FuzzSelectPages(f *testing.F) {
	f.Add(1, 3, 5, 0, 10)
	f.Add(0, 0, 0, 0, 0)
	f.Add(7, 2, 3, 9, 4)
	f.Fuzz(func(t *testing.T, a, b, c, d, n int) {
		if n < 0 || n > 10000 {
			return
		}
		var ranges []PageRange
		// Only valid ranges reach SelectPages (Settings.validate).
		for _, r := range []PageRange{{a, b}, {c, d}} {
			if r.From >= 1 && r.From <= 1e6 && (r.To == 0 || (r.To >= r.From && r.To <= 1e6)) {
				ranges = append(ranges, r)
			}
		}
		pages, _ := SelectPages(ranges, n)
		for _, p := range pages {
			if p < 0 || p >= n {
				t.Fatalf("SelectPages(%v, %d) yields page index %d", ranges, n, p)
			}
		}
		if len(ranges) == 0 && len(pages) != n {
			t.Fatalf("no ranges: %d pages, want %d", len(pages), n)
		}
	})
}
