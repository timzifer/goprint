package goprint

import (
	"reflect"
	"strings"
	"testing"
)

func TestToIOSPrintInfo(t *testing.T) {
	info, w := toIOSPrintInfo(Settings{Printer: "ipp://p.local/ipp/print", Duplex: DuplexShortEdge, Orientation: Landscape, Color: Monochrome, Quality: QualityHigh}, "Report")
	want := iosPrintInfo{jobName: "Report", printerID: "ipp://p.local/ipp/print", duplex: 2, orientation: 1, outputType: 3}
	if info != want || len(w) != 0 {
		t.Errorf("info %+v, warnings %v", info, w)
	}

	info, w = toIOSPrintInfo(Settings{Copies: 2, PageRanges: []PageRange{{From: 1}}, Media: MediaA4, Tray: "manual",
		Quality: QualityDraft, Orientation: ReverseLandscape, Scaling: ScalingFit, Vendor: map[string]string{"x": "y"}}, "t")
	if info.duplex != -1 || info.orientation != 1 || info.outputType != 0 {
		t.Errorf("defaults: %+v", info)
	}
	var got []string
	for _, x := range w {
		got = append(got, x.Setting)
	}
	if strings.Join(got, ",") != "Orientation,Quality,Copies,PageRanges,Media,Tray,Scaling,Vendor[x]" {
		t.Errorf("warnings %v", got)
	}
}

func TestFromIOSPrintInfo(t *testing.T) {
	in := Settings{Copies: 2, Color: Monochrome, Orientation: Landscape}
	got := fromIOSPrintInfo(in, iosPrintInfo{printerID: "ipp://x/ipp/print", duplex: 1, orientation: 0, outputType: 0})
	want := Settings{Printer: "ipp://x/ipp/print", Copies: 2, Duplex: DuplexLongEdge, Color: ColorAuto, Orientation: Portrait}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	// The picker reports no duplex (-1): the setting stays.
	got = fromIOSPrintInfo(Settings{Duplex: DuplexNone}, iosPrintInfo{duplex: -1, outputType: 2})
	if got.Duplex != DuplexNone || got.Color != Monochrome {
		t.Errorf("picker result %+v", got)
	}
}
