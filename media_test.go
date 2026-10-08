package goprint

import (
	"errors"
	"testing"
)

func TestParseMedia(t *testing.T) {
	for _, want := range []Media{MediaA3, MediaA4, MediaA5, MediaLetter, MediaLegal} {
		got, err := ParseMedia(want.Name)
		if err != nil {
			t.Fatalf("ParseMedia(%q): %v", want.Name, err)
		}
		if got != want {
			t.Errorf("ParseMedia(%q) = %+v, want %+v", want.Name, got, want)
		}
	}
}

func TestParseMediaInvalid(t *testing.T) {
	for _, name := range []string{
		"", "a4", "iso_a4", "a4_210x297mm", "iso_a4_210x297cm", "iso_a4_210mm",
		"iso_a4_0x297mm", "iso_a4_-1x297mm", "iso_a4_axbmm", "_x_1x1mm", "iso__1x1mm",
		"iso_a4_NaNx1mm", "iso_a4_1xInfmm", "iso_a4_1e6x1mm", "0_0_1e-7x1mm",
	} {
		if _, err := ParseMedia(name); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseMedia(%q) err = %v, want ErrInvalid", name, err)
		}
	}
}

func FuzzParseMedia(f *testing.F) {
	f.Add("iso_a4_210x297mm")
	f.Add("na_letter_8.5x11in")
	f.Add("custom_max_1e5x1e5mm")
	f.Fuzz(func(t *testing.T, name string) {
		m, err := ParseMedia(name)
		if err != nil {
			return
		}
		if m.Width <= 0 || m.Height <= 0 {
			t.Fatalf("ParseMedia(%q) = %+v: non-positive size", name, m)
		}
		if m.Name != name {
			t.Fatalf("ParseMedia(%q) changed name to %q", name, m.Name)
		}
	})
}

func TestMediaBySize(t *testing.T) {
	if m := mediaBySize(210000, 297000, "A4"); m != MediaA4 {
		t.Errorf("A4 → %+v", m)
	}
	if m := mediaBySize(279400, 215900, "Letter Rotated"); m != MediaLetter {
		t.Errorf("rotated letter → %+v", m)
	}
	m := mediaBySize(100000, 150500, "Etikett 100 × 150")
	if m.Name != "custom_etikett-100-150_100x150.5mm" || m.Width != 100000 || m.Height != 150500 {
		t.Errorf("custom → %+v", m)
	}
	if _, err := ParseMedia(m.Name); err != nil {
		t.Errorf("custom name not parseable: %v", err)
	}
	for _, n := range standardMedia {
		if _, err := ParseMedia(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func FuzzMediaBySize(f *testing.F) {
	f.Add(210000, 297000, "A4")
	f.Add(100000, 150500, "Etikett 100 × 150")
	f.Add(1, 1, "")
	f.Fuzz(func(t *testing.T, w, h int, name string) {
		if w <= 0 || h <= 0 || w > 1e8 || h > 1e8 {
			return
		}
		m := mediaBySize(w, h, name)
		p, err := ParseMedia(m.Name)
		if err != nil {
			t.Fatalf("mediaBySize(%d, %d, %q) = %q, not parseable: %v", w, h, name, m.Name, err)
		}
		if abs(p.Width-m.Width) > 1000 || abs(p.Height-m.Height) > 1000 {
			t.Fatalf("name %q encodes %dx%d, media is %dx%d", m.Name, p.Width, p.Height, m.Width, m.Height)
		}
	})
}
