package goprint

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Media is a paper size, identified by its PWG 5101.1 self-describing name
// (e.g. "iso_a4_210x297mm"). Width and Height are in micrometers.
type Media struct {
	Name          string
	Width, Height int
}

// Common media.
var (
	MediaA3     = Media{"iso_a3_297x420mm", 297000, 420000}
	MediaA4     = Media{"iso_a4_210x297mm", 210000, 297000}
	MediaA5     = Media{"iso_a5_148x210mm", 148000, 210000}
	MediaLetter = Media{"na_letter_8.5x11in", 215900, 279400}
	MediaLegal  = Media{"na_legal_8.5x14in", 215900, 355600}
)

// ParseMedia parses a PWG 5101.1 self-describing media name of the form
// "class_name_WxHunit" where unit is "mm" or "in".
func ParseMedia(name string) (Media, error) {
	parts := strings.Split(name, "_")
	if len(parts) < 3 || slices.Contains(parts, "") {
		return Media{}, invalidf("media: malformed PWG name %q", name)
	}
	dim := parts[len(parts)-1]
	var unit float64
	switch {
	case strings.HasSuffix(dim, "mm"):
		unit, dim = 1000, strings.TrimSuffix(dim, "mm")
	case strings.HasSuffix(dim, "in"):
		unit, dim = 25400, strings.TrimSuffix(dim, "in")
	default:
		return Media{}, invalidf("media: unknown unit in %q", name)
	}
	ws, hs, ok := strings.Cut(dim, "x")
	if !ok {
		return Media{}, invalidf("media: missing dimensions in %q", name)
	}
	w, err1 := strconv.ParseFloat(ws, 64)
	h, err2 := strconv.ParseFloat(hs, 64)
	if err1 != nil || err2 != nil || !(w > 0 && w <= 1e5) || !(h > 0 && h <= 1e5) {
		return Media{}, invalidf("media: invalid dimensions in %q", name)
	}
	m := Media{Name: name, Width: int(w*unit + 0.5), Height: int(h*unit + 0.5)}
	if m.Width < 1 || m.Height < 1 {
		return Media{}, invalidf("media: dimensions below 1µm in %q", name)
	}
	return m, nil
}

func (m Media) String() string {
	if m.Name != "" {
		return m.Name
	}
	return fmt.Sprintf("custom_%dx%dum", m.Width, m.Height)
}
