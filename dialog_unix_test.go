//go:build linux || freebsd || openbsd || netbsd || dragonfly

package goprint

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/timzifer/goprint/internal/portal/portaltest"
)

func TestPortalSettingsRoundTrip(t *testing.T) {
	yes := true
	for _, s := range []Settings{
		{},
		{Printer: "Office", Copies: 3, Collate: &yes, PageRanges: []PageRange{{1, 2}, {5, 0}}, Media: MediaA4,
			Orientation: Landscape, Duplex: DuplexLongEdge, Color: Monochrome, Quality: QualityHigh, Tray: "tray-2"},
		{Media: MediaLetter, Orientation: ReversePortrait, Duplex: DuplexShortEdge, Color: Color, Quality: QualityDraft},
	} {
		p, w := toPortalSettings(s)
		if len(w) != 0 {
			t.Errorf("%+v: warnings %v", s, w)
		}
		if got := fromPortalSettings(p, Settings{}); !reflect.DeepEqual(got, s) {
			t.Errorf("round trip\n got %+v\nwant %+v", got, s)
		}
	}
	p, _ := toPortalSettings(Settings{PageRanges: []PageRange{{2, 3}}, Duplex: DuplexLongEdge})
	if p.Print["page-ranges"] != "1-2" || p.Print["duplex"] != "horizontal" {
		t.Errorf("gtk values %v", p.Print)
	}
	_, w := toPortalSettings(Settings{Scaling: ScalingFit, Vendor: map[string]string{"gtk:output-bin": "top", "x": "y"}})
	if len(w) != 2 {
		t.Errorf("warnings = %v", w)
	}
}

func TestParseGTKRanges(t *testing.T) {
	got := parseGTKRanges("0-1, 4 ,6-1073741823,bad")
	want := []PageRange{{1, 2}, {5, 5}, {7, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func portalDoc() Document {
	return pdfDoc("portal doc")
}

func TestPortalDialogSettingsOnly(t *testing.T) {
	p := portaltest.Start(t)
	p.SetChoose(func(s map[string]string, ps map[string]dbus.Variant) (map[string]string, map[string]dbus.Variant) {
		s["printer"] = "Chosen"
		s["n-copies"] = "4"
		ps["Orientation"] = dbus.MakeVariant("portrait")
		return s, ps
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	job, chosen, err := Dialog(ctx, portalDoc(), DialogOptions{
		Settings: Settings{Printer: "Office", Copies: 2, Orientation: Landscape, Media: MediaA5},
		Owner:    0x4a00001,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Error("job without PrintNow")
	}
	if chosen.Printer != "Chosen" || chosen.Copies != 4 || chosen.Orientation != Portrait || chosen.Media != MediaA5 {
		t.Errorf("chosen %+v", chosen)
	}
	prepared, printed := p.Calls()
	if len(prepared) != 1 || len(printed) != 0 {
		t.Fatalf("calls: %d prepared, %d printed", len(prepared), len(printed))
	}
	pr := prepared[0]
	if pr.Parent != "x11:4a00001" || pr.Title != "portal doc" || pr.Settings["printer"] != "Office" || pr.Settings["n-copies"] != "2" || pr.Settings["orientation"] != "landscape" {
		t.Errorf("prepared %+v", pr)
	}
	if w, _ := pr.PageSetup["Width"].Value().(float64); w != 148 {
		t.Errorf("page setup width %v", pr.PageSetup["Width"])
	}
}

func TestPortalDialogPrint(t *testing.T) {
	p := portaltest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	doc := portalDoc()
	job, _, err := Dialog(ctx, doc, DialogOptions{PrintNow: true})
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		t.Fatal("no job")
	}
	if err := job.Wait(ctx); err != nil {
		t.Errorf("Wait: %v", err)
	}
	_, printed := p.Calls()
	if len(printed) != 1 {
		t.Fatalf("%d print calls", len(printed))
	}
	src, _ := doc.open()
	var want bytes.Buffer
	_, _ = want.ReadFrom(src)
	if !bytes.Equal(printed[0].Document, want.Bytes()) {
		t.Errorf("portal received %d bytes, want the %d-byte PDF", len(printed[0].Document), want.Len())
	}
	if printed[0].Token != 1 {
		t.Errorf("token %d, want the one from PreparePrint", printed[0].Token)
	}
}

func TestPortalDialogCancel(t *testing.T) {
	p := portaltest.Start(t)
	p.SetResponse(1)
	_, _, err := Dialog(context.Background(), portalDoc(), DialogOptions{PrintNow: true})
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if _, printed := p.Calls(); len(printed) != 0 {
		t.Error("printed after cancel")
	}
}

func TestPortalRequirePrinter(t *testing.T) {
	p := portaltest.Start(t)
	p.SetChoose(func(s map[string]string, ps map[string]dbus.Variant) (map[string]string, map[string]dbus.Variant) {
		s["printer"] = "Other"
		return s, ps
	})
	_, _, err := Dialog(context.Background(), portalDoc(), DialogOptions{Settings: Settings{Printer: "Office"}, RequirePrinter: true, PrintNow: true})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if _, printed := p.Calls(); len(printed) != 0 {
		t.Error("printed on another printer")
	}
}

func TestPortalMissing(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/goprint-bus")
	_, _, err := Dialog(context.Background(), portalDoc(), DialogOptions{})
	if !errors.Is(err, ErrNoDialog) {
		t.Fatalf("err = %v, want ErrNoDialog", err)
	}
}

func FuzzParseGTKRanges(f *testing.F) {
	f.Add("0-1,4,6-1073741823")
	f.Add("-,,3-")
	f.Fuzz(func(t *testing.T, s string) {
		for _, r := range parseGTKRanges(s) {
			// Ranges handed back to the caller must pass Settings.validate.
			if r.From < 1 || (r.To != 0 && r.To < r.From) {
				t.Fatalf("parseGTKRanges(%q) yields invalid range %+v", s, r)
			}
		}
	})
}
