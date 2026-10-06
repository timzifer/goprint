//go:build windows

package goprint

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/testpdf"
	"github.com/timzifer/goprint/internal/winprint"
)

// TestDialogPublic opens the dialog through the public API; close it to
// pass. Set GOPRINT_DIALOG=1.
func TestDialogPublic(t *testing.T) {
	if os.Getenv("GOPRINT_DIALOG") == "" {
		t.Skip("set GOPRINT_DIALOG=1")
	}
	src := testpdf.Generate(2, testpdf.A4Width, testpdf.A4Height)
	doc := Document{Title: "goprint public dialog", PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	job, chosen, err := Dialog(ctx, doc, DialogOptions{Settings: Settings{Copies: 2, Orientation: Landscape}})
	if errors.Is(err, ErrCanceled) {
		t.Log("canceled")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("job %v, chosen %+v", job, chosen)
}

func TestTaskOptionsRoundTrip(t *testing.T) {
	yes, no := true, false
	for _, s := range []Settings{
		{},
		{Copies: 3, Media: MediaA4, Orientation: Landscape, Duplex: DuplexLongEdge, Color: Monochrome, Collate: &yes, Quality: QualityHigh},
		{Media: MediaLetter, Orientation: ReversePortrait, Duplex: DuplexShortEdge, Color: Color, Collate: &no, Quality: QualityDraft},
		{Orientation: ReverseLandscape, Duplex: DuplexNone, Quality: QualityNormal},
	} {
		o, w := toTaskOptions(s)
		if len(w) != 0 {
			t.Errorf("%+v: unexpected warnings %v", s, w)
		}
		if got := fromTaskOptions(o, Settings{}); !reflect.DeepEqual(got, s) {
			t.Errorf("round trip\n got %+v\nwant %+v", got, s)
		}
	}
}

func TestTaskOptionsWarnings(t *testing.T) {
	_, w := toTaskOptions(Settings{
		Printer:    "x",
		Media:      Media{Name: "iso_c5_162x229mm"},
		PageRanges: []PageRange{{1, 2}},
		Scaling:    ScalingFit,
		Tray:       "manual",
		Vendor:     map[string]string{"k": "v"},
	})
	got := map[string]bool{}
	for _, x := range w {
		got[x.Setting] = true
	}
	for _, name := range []string{"Printer", "Media", "PageRanges", "Scaling", "Tray", "Vendor[k]"} {
		if !got[name] {
			t.Errorf("no warning for %s (got %v)", name, w)
		}
	}
}

func TestWRTMediaNamesValid(t *testing.T) {
	for name := range wrtMediaSizes {
		if _, err := ParseMedia(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestFromTaskOptionsKeepsPresets(t *testing.T) {
	preset := Settings{Printer: "p", Scaling: ScalingFit, Tray: "t"}
	got := fromTaskOptions(winprint.TaskOptions{}, preset)
	if got.Printer != "" || got.Scaling != ScalingFit || got.Tray != "t" {
		t.Errorf("got %+v", got)
	}
}
