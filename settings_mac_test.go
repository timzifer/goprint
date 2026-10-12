package goprint

import (
	"math"
	"reflect"
	"testing"

	"github.com/timzifer/goprint/internal/macprint"
)

func TestToMacOptions(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name     string
		s        Settings
		want     macprint.Options
		warnings []string
	}{
		{"zero", Settings{}, macprint.Options{Scaling: macprint.ScaleDownToFit, AutoRotate: true}, nil},
		{"basic", Settings{Printer: "Office", Copies: 3, Collate: &yes, Duplex: DuplexLongEdge, Color: Monochrome},
			macprint.Options{Printer: "Office", Copies: 3, Collate: macprint.Yes, Duplex: macprint.DuplexLongEdge,
				Color: macprint.No, Scaling: macprint.ScaleDownToFit, AutoRotate: true}, nil},
		{"uncollated color short edge", Settings{Collate: &no, Duplex: DuplexShortEdge, Color: Color},
			macprint.Options{Collate: macprint.No, Duplex: macprint.DuplexShortEdge, Color: macprint.Yes,
				Scaling: macprint.ScaleDownToFit, AutoRotate: true}, nil},
		{"simplex", Settings{Duplex: DuplexNone},
			macprint.Options{Duplex: macprint.DuplexNone, Scaling: macprint.ScaleDownToFit, AutoRotate: true}, nil},
		{"page range", Settings{PageRanges: []PageRange{{2, 3}}},
			macprint.Options{FirstPage: 2, LastPage: 3, Scaling: macprint.ScaleDownToFit, AutoRotate: true}, nil},
		{"open range", Settings{PageRanges: []PageRange{{4, 0}}},
			macprint.Options{FirstPage: 4, Scaling: macprint.ScaleDownToFit, AutoRotate: true}, nil},
		{"all pages range", Settings{PageRanges: []PageRange{{1, 0}}},
			macprint.Options{Scaling: macprint.ScaleDownToFit, AutoRotate: true}, nil},
		{"several ranges", Settings{PageRanges: []PageRange{{1, 2}, {5, 5}}},
			macprint.Options{FirstPage: 1, LastPage: 2, Scaling: macprint.ScaleDownToFit, AutoRotate: true},
			[]string{"PageRanges"}},
		{"landscape", Settings{Orientation: Landscape},
			macprint.Options{Orientation: macprint.OrientationLandscape, SetOrientation: true, Scaling: macprint.ScaleDownToFit}, nil},
		{"portrait", Settings{Orientation: Portrait},
			macprint.Options{Orientation: macprint.OrientationPortrait, SetOrientation: true, Scaling: macprint.ScaleDownToFit}, nil},
		{"reverse landscape", Settings{Orientation: ReverseLandscape},
			macprint.Options{Orientation: macprint.OrientationLandscape, SetOrientation: true, Scaling: macprint.ScaleDownToFit},
			[]string{"Orientation"}},
		{"scaling none", Settings{Scaling: ScalingNone}, macprint.Options{Scaling: macprint.ScaleNone, AutoRotate: true}, nil},
		{"scaling fit", Settings{Scaling: ScalingFit}, macprint.Options{Scaling: macprint.ScaleToFit, AutoRotate: true}, nil},
		{"scaling fill", Settings{Scaling: ScalingFill}, macprint.Options{Scaling: macprint.ScaleToFit, AutoRotate: true},
			[]string{"Scaling"}},
		{"uri printer", Settings{Printer: "ipp://printer.local/ipp/print"},
			macprint.Options{Scaling: macprint.ScaleDownToFit, AutoRotate: true}, []string{"Printer"}},
		{"extras", Settings{Quality: QualityHigh, Tray: "manual", Vendor: map[string]string{"print-scaling": "fill", "windows:x": "y"}},
			macprint.Options{Scaling: macprint.ScaleDownToFit, AutoRotate: true,
				Extra: map[string]string{"print-quality": "5", "media-source": "manual", "print-scaling": "fill"}},
			[]string{"Vendor[windows:x]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, w := toMacOptions(tt.s, "")
			if tt.want.Extra == nil {
				tt.want.Extra = map[string]string{}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("toMacOptions =\n%+v, want\n%+v", got, tt.want)
			}
			var names []string
			for _, x := range w {
				names = append(names, x.Setting)
			}
			if !reflect.DeepEqual(names, tt.warnings) {
				t.Errorf("warnings = %v, want %v", w, tt.warnings)
			}
		})
	}
}

func TestToMacOptionsMedia(t *testing.T) {
	for _, tc := range []struct {
		m    Media
		w, h float64
	}{
		{MediaA4, 595.276, 841.89},
		{MediaA5, 419.528, 595.276},
		{MediaLetter, 612, 792},
		{Media{Name: "iso_a3_297x420mm"}, 841.89, 1190.551},
		{Media{Width: 100000, Height: 50000}, 283.465, 141.732},
	} {
		o, _ := toMacOptions(Settings{Media: tc.m}, "")
		if math.Abs(o.PaperWidth-tc.w) > 0.01 || math.Abs(o.PaperHeight-tc.h) > 0.01 {
			t.Errorf("%v: paper %.3fx%.3f pt, want %.3fx%.3f", tc.m, o.PaperWidth, o.PaperHeight, tc.w, tc.h)
		}
	}
}

func TestFromMacResult(t *testing.T) {
	yes, no := true, false
	preset := Settings{Copies: 2, Vendor: map[string]string{"k": "v"}, Quality: QualityDraft}
	tests := []struct {
		name   string
		r      macprint.Result
		preset Settings
		want   Settings
	}{
		{"a4 portrait all pages",
			macprint.Result{Printer: "Office", PaperName: "iso-a4", PaperWidth: 595.28, PaperHeight: 841.89,
				Copies: 3, AllPages: true, Collate: macprint.Yes, Duplex: macprint.DuplexLongEdge, ColorModel: "Gray"},
			preset,
			Settings{Printer: "Office", Copies: 3, Media: MediaA4, Orientation: Portrait, Collate: &yes,
				Duplex: DuplexLongEdge, Color: Monochrome, Vendor: preset.Vendor, Quality: QualityDraft}},
		{"a5 landscape range",
			macprint.Result{PaperName: "iso-a5", PaperWidth: 595.28, PaperHeight: 419.53, Orientation: macprint.OrientationLandscape,
				FirstPage: 2, LastPage: 3, Collate: macprint.No, Duplex: macprint.DuplexNone, PrintColorMode: "color"},
			Settings{PageRanges: []PageRange{{1, 1}}},
			Settings{Media: MediaA5, Orientation: Landscape, PageRanges: []PageRange{{2, 3}}, Collate: &no,
				Duplex: DuplexNone, Color: Color}},
		{"reverse kept",
			macprint.Result{Orientation: macprint.OrientationLandscape, AllPages: true, Duplex: macprint.DuplexShortEdge},
			Settings{Orientation: ReverseLandscape, PageRanges: []PageRange{{1, 1}}},
			Settings{Orientation: ReverseLandscape, Duplex: DuplexShortEdge}},
		{"custom paper",
			macprint.Result{PaperName: "My Paper", PaperWidth: 283.46, PaperHeight: 141.73, AllPages: true},
			Settings{},
			Settings{Media: Media{Name: "custom_my-paper_50x100mm", Width: 50000, Height: 100000}, Orientation: Portrait}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromMacResult(tt.r, tt.preset)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fromMacResult =\n%+v, want\n%+v", got, tt.want)
			}
		})
	}
}

func TestMacColor(t *testing.T) {
	for _, tc := range []struct {
		model, mode string
		want        ColorMode
		ok          bool
	}{
		{"Gray", "", Monochrome, true},
		{"CMYK", "", Color, true},
		{"RGB", "monochrome", Color, true},
		{"", "monochrome", Monochrome, true},
		{"", "color", Color, true},
		{"", "auto", ColorAuto, false},
		{"Unknown", "", ColorAuto, false},
	} {
		if got, ok := macColor(tc.model, tc.mode); got != tc.want || ok != tc.ok {
			t.Errorf("macColor(%q, %q) = %v, %v; want %v, %v", tc.model, tc.mode, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPickSpooledJob(t *testing.T) {
	jobs := []spooledJob{{3, "doc", "A"}, {7, "other", "A"}, {8, "doc", "B"}, {9, "doc", "B"}, {2, "doc", "A"}}
	tests := []struct {
		name     string
		jobs     []spooledJob
		baseline int
		title    string
		want     int
		ok       bool
	}{
		{"newest named", jobs, 5, "doc", 9, true},
		{"single new unnamed", jobs[:2], 5, "doc", 7, true},
		{"ambiguous", jobs, 5, "missing", 0, false},
		{"none new", jobs, 10, "doc", 0, false},
		{"empty", nil, 0, "doc", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pickSpooledJob(tt.jobs, tt.baseline, tt.title)
			if got.ID != tt.want || ok != tt.ok {
				t.Errorf("pickSpooledJob = %+v, %v; want id %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestNeedsPDFKitLayout(t *testing.T) {
	tests := []struct {
		name string
		s    Settings
		want bool
	}{
		{"defaults", Settings{Copies: 2, Media: MediaA4}, false},
		{"landscape", Settings{Orientation: Landscape}, true},
		{"portrait", Settings{Orientation: Portrait}, true},
		{"scaling", Settings{Scaling: ScalingFit}, true},
		{"one range", Settings{Orientation: Landscape, PageRanges: []PageRange{{From: 2, To: 3}}}, true},
		{"several ranges", Settings{Orientation: Landscape, PageRanges: []PageRange{{From: 1, To: 1}, {From: 3, To: 3}}}, false},
		{"strict", Settings{Orientation: Landscape, Strict: true}, false},
		{"credentials", Settings{Orientation: Landscape, Credentials: &Credentials{Username: "u"}}, false},
		{"uri", Settings{Orientation: Landscape, Printer: "ipp://host/ipp/print"}, false},
	}
	for _, tt := range tests {
		if got := needsPDFKitLayout(tt.s); got != tt.want {
			t.Errorf("%s: needsPDFKitLayout = %v; want %v", tt.name, got, tt.want)
		}
	}
}
