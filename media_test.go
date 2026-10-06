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
		"iso_a4_NaNx1mm", "iso_a4_1xInfmm", "iso_a4_1e6x1mm",
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
