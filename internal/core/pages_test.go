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
