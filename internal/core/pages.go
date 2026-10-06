package core

// PageRange is an inclusive, 1-based page range; To == 0 means "to the end".
type PageRange struct{ From, To int }

// SelectPages returns the 0-based page indexes selected by ranges for a
// document with n pages, in range order (duplicates allowed, as with
// "1-2,1"). No ranges select all pages. Pages beyond n are dropped and
// reported via clipped.
func SelectPages(ranges []PageRange, n int) (pages []int, clipped bool) {
	if len(ranges) == 0 {
		pages = make([]int, n)
		for i := range pages {
			pages[i] = i
		}
		return pages, false
	}
	for _, r := range ranges {
		to := r.To
		if to == 0 || to > n {
			clipped = clipped || (r.To != 0 && r.To > n)
			to = n
		}
		if r.From > n {
			clipped = true
			continue
		}
		for p := max(r.From, 1); p <= to; p++ {
			pages = append(pages, p-1)
		}
	}
	return pages, clipped
}
