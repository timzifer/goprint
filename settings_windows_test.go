//go:build windows

package goprint

import (
	"reflect"
	"testing"
)

func TestJobSettingsRoundTrip(t *testing.T) {
	yes := true
	for _, s := range []Settings{
		{},
		{Copies: 2, Collate: &yes, Media: MediaA5, Orientation: Landscape, Duplex: DuplexLongEdge, Color: Monochrome, Quality: QualityHigh, Tray: "Fach 1"},
		{Media: MediaLetter, Orientation: Portrait, Duplex: DuplexShortEdge, Color: Color, Quality: QualityDraft},
	} {
		j, w := toJobSettings(s)
		if len(w) != 0 {
			t.Errorf("%+v: warnings %v", s, w)
		}
		if got := fromJobSettings(j, Settings{}); !reflect.DeepEqual(got, s) {
			t.Errorf("round trip\n got %+v\nwant %+v", got, s)
		}
	}
	_, w := toJobSettings(Settings{Orientation: ReverseLandscape, Vendor: map[string]string{"x": "1"}})
	if len(w) != 2 {
		t.Errorf("warnings = %v", w)
	}
}
