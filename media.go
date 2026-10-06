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

// standardMedia are PWG 5101.1 names of common sizes, used to name paper
// that a platform reports only by its dimensions.
var standardMedia = []string{
	"iso_a0_841x1189mm", "iso_a1_594x841mm", "iso_a2_420x594mm", "iso_a3_297x420mm",
	"iso_a4_210x297mm", "iso_a5_148x210mm", "iso_a6_105x148mm", "iso_a7_74x105mm",
	"iso_b4_250x353mm", "iso_b5_176x250mm", "iso_b6_125x176mm",
	"iso_c4_229x324mm", "iso_c5_162x229mm", "iso_c6_114x162mm", "iso_dl_110x220mm",
	"jis_b4_257x364mm", "jis_b5_182x257mm", "jis_b6_128x182mm",
	"na_letter_8.5x11in", "na_legal_8.5x14in", "na_executive_7.25x10.5in",
	"na_ledger_11x17in", "na_invoice_5.5x8.5in", "na_govt-letter_8x10in",
	"na_number-10_4.125x9.5in", "na_monarch_3.875x7.5in", "na_index-4x6_4x6in",
	"na_5x7_5x7in", "oe_photo-l_3.5x5in", "na_foolscap_8.5x13in",
}

// mediaBySize returns the standard media with the given size (micrometers,
// either orientation, ±1 mm), or a PWG custom media named after name.
func mediaBySize(w, h int, name string) Media {
	lo, hi := min(w, h), max(w, h)
	for _, n := range standardMedia {
		m, err := ParseMedia(n)
		if err != nil {
			continue
		}
		if abs(m.Width-lo) <= 1000 && abs(m.Height-hi) <= 1000 {
			return m
		}
	}
	return Media{Name: customMediaName(name, lo, hi), Width: lo, Height: hi}
}

// customMediaName builds "custom_<name>_<w>x<h>mm".
func customMediaName(name string, w, h int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-' || r == '.':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	n := strings.Trim(b.String(), "-")
	if n == "" {
		n = "paper"
	}
	mm := func(v int) string { return strconv.FormatFloat(float64(v)/1000, 'f', -1, 64) }
	return fmt.Sprintf("custom_%s_%sx%smm", n, mm(w), mm(h))
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
