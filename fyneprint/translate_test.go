package fyneprint

import (
	"errors"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/timzifer/goprint"
)

// upper is an app translator that marks every text it supplies.
func upper(key, fallback string, data any) string {
	return "[" + strings.ToUpper(RenderText(fallback, data)) + "]"
}

func TestTranslateOption(t *testing.T) {
	stubPrinters(t, testPrinters)
	var asked []string
	tr := func(key, fallback string, data any) string {
		asked = append(asked, key)
		return upper(key, fallback, data)
	}
	d, _ := openTestDialog(t, a4Doc(2), PrintDialogOptions{Translate: tr, Settings: goprint.Settings{Orientation: goprint.Landscape}})

	for name, got := range map[string]string{
		"ok":          d.ok.Text,
		"cancel":      d.cancel.Text,
		"save":        d.save.Text,
		"copies row":  d.copiesItem.Text,
		"collate":     d.collate.Text,
		"orientation": d.orientation.Selected,
		"printer":     d.printer.Selected,
		"sheet":       d.pageLabel.Text,
	} {
		if !strings.HasPrefix(got, "[") {
			t.Errorf("%s: %q not from the translator", name, got)
		}
	}
	if d.orientation.SelectedIndex() != int(goprint.Landscape) {
		t.Errorf("orientation index %d", d.orientation.SelectedIndex())
	}
	if d.printer.Selected != "[OFFICE (DEFAULT)]" {
		t.Errorf("templated printer label %q", d.printer.Selected)
	}
	for _, k := range []string{"OK", "Cancel", "fyneprint.copies", "fyneprint.duplex.long"} {
		if !strings.Contains(strings.Join(asked, " "), k) {
			t.Errorf("translator never asked for %s", k)
		}
	}

	// Input errors are texts of the dialog too.
	d.ranges.SetText("3-1")
	if d.status.Text != `[INVALID PAGE RANGE 3-1]` || !errors.Is(d.inputErr, goprint.ErrInvalid) {
		t.Errorf("status %q, err %v", d.status.Text, d.inputErr)
	}
}

func TestTranslatorFallbackAndPrecedence(t *testing.T) {
	stubPrinters(t, testPrinters)
	SetTranslator(func(key, fallback string, data any) string {
		if key == "fyneprint.copies" {
			return "app copies"
		}
		if key == "fyneprint.collate" {
			return "app collate"
		}
		return "" // keep the built-in text
	})
	t.Cleanup(func() { SetTranslator(nil) })
	only := func(key, fallback string, data any) string {
		if key == "fyneprint.collate" {
			return "dialog collate"
		}
		return ""
	}
	d, _ := openTestDialog(t, a4Doc(1), PrintDialogOptions{Translate: only})
	if d.copiesItem.Text != "app copies" || d.collate.Text != "dialog collate" {
		t.Errorf("copies %q, collate %q", d.copiesItem.Text, d.collate.Text)
	}
	if want := d.t("scaling"); d.scalingItem.Text != want || strings.HasPrefix(want, "app") {
		t.Errorf("scaling %q", d.scalingItem.Text)
	}
}

func TestRefreshTexts(t *testing.T) {
	stubPrinters(t, testPrinters)
	lang := "en"
	SetTranslator(func(key, fallback string, data any) string {
		if lang == "xx" {
			return upper(key, fallback, data)
		}
		return RenderText(fallback, data)
	})
	t.Cleanup(func() { SetTranslator(nil) })
	d, r := openTestDialog(t, a4Doc(3), PrintDialogOptions{Settings: goprint.Settings{Duplex: goprint.DuplexLongEdge, Tray: "tray-1"}})
	d.ranges.SetText("2-3")
	d.paper.SetSelectedIndex(2)
	if d.copiesItem.Text != "Copies" {
		t.Fatalf("copies %q", d.copiesItem.Text)
	}

	lang = "xx"
	RefreshTexts()
	if d.copiesItem.Text != "[COPIES]" || d.ok.Text != "[OK]" || d.pageLabel.Text != "[SHEET 1 OF 2 (PAGE 2)]" {
		t.Errorf("after refresh: copies %q, ok %q, sheet %q", d.copiesItem.Text, d.ok.Text, d.pageLabel.Text)
	}
	if d.allPagesSelected() || d.duplex.SelectedIndex() != int(goprint.DuplexLongEdge) || d.paper.SelectedIndex() != 2 ||
		d.currentTray() != "tray-1" || d.s.Printer != "Office" {
		t.Errorf("selection lost: all pages %v, duplex %d, paper %d, tray %q, printer %q",
			d.allPagesSelected(), d.duplex.SelectedIndex(), d.paper.SelectedIndex(), d.currentTray(), d.s.Printer)
	}
	test.Tap(d.ok)
	if r.err != nil || len(r.s.PageRanges) != 1 || r.s.Duplex != goprint.DuplexLongEdge {
		t.Errorf("result %+v", r)
	}
	if openDialogs[d] {
		t.Error("closed dialog still registered")
	}
}

func TestRenderText(t *testing.T) {
	for _, tc := range []struct {
		text string
		data any
		want string
	}{
		{"Tray {{.N}}", map[string]any{"N": "2"}, "Tray 2"},
		{"plain", nil, "plain"},
		{"{{.Missing", map[string]any{}, "{{.Missing"},
	} {
		if got := RenderText(tc.text, tc.data); got != tc.want {
			t.Errorf("RenderText(%q) = %q", tc.text, got)
		}
	}
	keys := TranslationKeys()
	if keys["OK"] != "OK" || keys["fyneprint.sheet"] == "" {
		t.Errorf("TranslationKeys misses entries: %d keys", len(keys))
	}
	keys["OK"] = "changed"
	if TranslationKeys()["OK"] != "OK" {
		t.Error("TranslationKeys returns the internal map")
	}
}
