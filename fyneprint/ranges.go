package fyneprint

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/timzifer/goprint"
)

// parseRanges parses a page selection as typed by users: "1-3, 5, 8-"
// (an open end runs to the last page). Pages are 1-based. An empty or
// blank string selects all pages (nil).
func parseRanges(s string) ([]goprint.PageRange, error) {
	var out []goprint.PageRange
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, isRange := strings.Cut(part, "-")
		f, err := pageNumber(from)
		if err != nil {
			return nil, fmt.Errorf("invalid page range %q", part)
		}
		r := goprint.PageRange{From: f, To: f}
		if isRange {
			if to = strings.TrimSpace(to); to == "" {
				r.To = 0
			} else if r.To, err = pageNumber(to); err != nil || r.To < r.From {
				return nil, fmt.Errorf("invalid page range %q", part)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func pageNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid page number %q", s)
	}
	return n, nil
}

// formatRanges is the inverse of parseRanges.
func formatRanges(rs []goprint.PageRange) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		switch {
		case r.To == 0:
			parts = append(parts, fmt.Sprintf("%d-", r.From))
		case r.To == r.From:
			parts = append(parts, strconv.Itoa(r.From))
		default:
			parts = append(parts, fmt.Sprintf("%d-%d", r.From, r.To))
		}
	}
	return strings.Join(parts, ", ")
}
