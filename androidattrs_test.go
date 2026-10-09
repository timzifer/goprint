package goprint

import (
	"reflect"
	"strings"
	"testing"
)

func TestToAndroidAttrs(t *testing.T) {
	a, w := toAndroidAttrs(Settings{Color: Monochrome, Duplex: DuplexShortEdge, Orientation: Landscape})
	if a != (androidAttrs{color: androidColorMono, duplex: androidDuplexShort, orientation: 1}) || len(w) != 0 {
		t.Errorf("attrs %+v, warnings %v", a, w)
	}
	a, w = toAndroidAttrs(Settings{Printer: "Office", Copies: 3, PageRanges: []PageRange{{From: 2}}, Media: MediaA4,
		Tray: "manual", Quality: QualityHigh, Scaling: ScalingFit, Orientation: ReversePortrait, Vendor: map[string]string{"k": "v"}})
	if a != (androidAttrs{orientation: 2}) {
		t.Errorf("attrs %+v", a)
	}
	var got []string
	for _, x := range w {
		got = append(got, x.Setting)
	}
	if strings.Join(got, ",") != "Orientation,Printer,Copies,PageRanges,Media,Tray,Quality,Scaling,Vendor[k]" {
		t.Errorf("warnings %v", got)
	}
}

func TestAndroidResultSettings(t *testing.T) {
	// A4 landscape (8268 × 11693 mils), 2 copies, color, long edge, pages 1-2 and 5.
	r := androidResult{androidPending, 2, androidColorColor, androidDuplexLong, 8268, 11693, 1, 2, 0, 1, 4, 4}
	got := r.settings(Settings{Provider: "", Quality: QualityHigh})
	want := Settings{Copies: 2, Color: Color, Duplex: DuplexLongEdge, Media: MediaA4, Orientation: Landscape,
		PageRanges: []PageRange{{From: 1, To: 2}, {From: 5, To: 5}}, Quality: QualityHigh}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	// Nothing known: the settings stay.
	in := Settings{Copies: 4, Duplex: DuplexNone}
	if got := (androidResult{androidCanceled}).settings(in); !reflect.DeepEqual(got, in) {
		t.Errorf("short result changed settings: %+v", got)
	}
}

func TestAndroidJobState(t *testing.T) {
	for st, want := range map[int]JobState{
		androidCanceled: JobCanceled, androidFailed: JobAborted, androidPending: JobPending,
		androidProcessing: JobProcessing, androidCompleted: JobCompleted,
	} {
		if got := androidJobState(st); got != want {
			t.Errorf("androidJobState(%d) = %v, want %v", st, got, want)
		}
	}
}
