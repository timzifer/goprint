package fyneprint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	pdf "github.com/timzifer/fyne-pdf"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/testpdf"
)

func TestParseRanges(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []goprint.PageRange
		err  bool
	}{
		{"", nil, false},
		{"  ", nil, false},
		{"3", []goprint.PageRange{{From: 3, To: 3}}, false},
		{"1-3, 5,8-", []goprint.PageRange{{From: 1, To: 3}, {From: 5, To: 5}, {From: 8, To: 0}}, false},
		{" 2 - 4 ,", []goprint.PageRange{{From: 2, To: 4}}, false},
		{"0", nil, true},
		{"4-2", nil, true},
		{"a", nil, true},
		{"-3", nil, true},
	} {
		got, err := parseRanges(tc.in)
		if (err != nil) != tc.err || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseRanges(%q) = %v, %v", tc.in, got, err)
		}
		if err == nil {
			back, _ := parseRanges(formatRanges(got))
			if !reflect.DeepEqual(back, got) {
				t.Errorf("round trip of %q: %v", tc.in, back)
			}
		}
	}
}

func TestTurnAngle(t *testing.T) {
	portrait, landscape := pageSize{595, 842}, pageSize{842, 595}
	for _, tc := range []struct {
		sz   pageSize
		o    goprint.Orientation
		want int
	}{
		{portrait, goprint.OrientationDefault, 0},
		{landscape, goprint.OrientationDefault, 0},
		{portrait, goprint.Portrait, 0},
		{landscape, goprint.Portrait, 270},
		{portrait, goprint.Landscape, 90},
		{landscape, goprint.Landscape, 0},
		{portrait, goprint.ReversePortrait, 180},
		{landscape, goprint.ReverseLandscape, 180},
		{landscape, goprint.ReversePortrait, 90},
		{portrait, goprint.ReverseLandscape, 270},
	} {
		if got := turnAngle(tc.sz, tc.o); got != tc.want {
			t.Errorf("turnAngle(%v, %v) = %d, want %d", tc.sz, tc.o, got, tc.want)
		}
	}
}

func TestPaperFor(t *testing.T) {
	page := pageSize{595, 842}
	if got := paperFor(page, goprint.Media{}, goprint.OrientationDefault); got != page {
		t.Errorf("no media: %v", got)
	}
	if got := paperFor(page, goprint.Media{}, goprint.Landscape); got != (pageSize{842, 595}) {
		t.Errorf("no media, landscape: %v", got)
	}
	got := paperFor(page, goprint.MediaA5, goprint.Landscape)
	if got.W < got.H || int(got.W) != 595 || int(got.H) != 419 {
		t.Errorf("A5 landscape: %v", got)
	}
}

func TestMediaLabel(t *testing.T) {
	for m, want := range map[goprint.Media]string{
		goprint.MediaA4:             "A4 (210 × 297 mm)",
		goprint.MediaLetter:         "Letter (8.5 × 11 in)",
		{Width: 1000, Height: 2000}: "custom_1000x2000um",
	} {
		if got := mediaLabel(m); got != want {
			t.Errorf("mediaLabel(%v) = %q, want %q", m, got, want)
		}
	}
}

func TestIsFilePrinter(t *testing.T) {
	for name, want := range map[string]bool{
		"Microsoft Print to PDF":        true,
		"Microsoft XPS Document Writer": true,
		"OneNote (Desktop)":             true,
		"PDF":                           true,
		"Cups-PDF":                      true,
		"SHARP BP-50M26 PCL6":           false,
		"Office PDF-Ready Laser":        false,
	} {
		if got := isFilePrinter(name); got != want {
			t.Errorf("isFilePrinter(%q) = %v", name, got)
		}
	}
}

func a4Doc(pages int) goprint.Document {
	data := testpdf.Generate(pages, testpdf.A4Width, testpdf.A4Height)
	return goprint.PDFBytes("Report", data)
}

func pdfPages(t *testing.T, b []byte) []pageSize {
	t.Helper()
	src, err := pdf.OpenSource(b)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out := make([]pageSize, src.PageCount())
	for i := range out {
		r, err := src.Bound(i)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = pageSize{float64(r.Dx()), float64(r.Dy())}
	}
	return out
}

func TestSavedPDF(t *testing.T) {
	data := testpdf.Generate(3, testpdf.A4Width, testpdf.A4Height)
	sizes := pdfPages(t, data)

	out, err := savedPDF(context.Background(), data, sizes, goprint.Settings{})
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("all pages: changed the PDF (%v)", err)
	}

	out, err = savedPDF(context.Background(), data, sizes, goprint.Settings{
		PageRanges:  []goprint.PageRange{{From: 3, To: 3}, {From: 1, To: 1}},
		Orientation: goprint.Landscape,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := pdfPages(t, out)
	if len(got) != 2 || got[0].W <= got[0].H || got[1].W <= got[1].H {
		t.Fatalf("pages 3,1 landscape: %v", got)
	}

	if _, err := savedPDF(context.Background(), data, sizes, goprint.Settings{PageRanges: []goprint.PageRange{{From: 5, To: 6}}}); !errors.Is(err, goprint.ErrInvalid) {
		t.Errorf("pages beyond the end: %v", err)
	}
}

func TestSavedPDFFromImages(t *testing.T) {
	doc := goprint.Document{Images: []image.Image{image.NewGray(image.Rect(0, 0, 20, 10)), image.NewGray(image.Rect(0, 0, 20, 10))}, DPI: 72}
	data, err := documentPDF(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := pdfPages(t, data); len(got) != 2 || got[0] != (pageSize{20, 10}) {
		t.Fatalf("image pages: %v", got)
	}
}

// fakePrinters stubs everything that would reach a printer and makes the
// dialog's background work synchronous.
type fakePrinters struct {
	printed []goprint.Settings
}

func stubPrinters(t *testing.T, list []goprint.Printer) *fakePrinters {
	t.Helper()
	f := &fakePrinters{}
	savedPrinters, savedCaps, savedPrint, savedProps, savedAsync := printersFunc, capsFunc, printFunc, propertiesFunc, runAsync
	printersFunc = func(context.Context) ([]goprint.Printer, error) { return list, nil }
	capsFunc = func(_ context.Context, name string) (goprint.Capabilities, error) {
		c := goprint.Capabilities{Media: []goprint.Media{goprint.MediaA4, goprint.MediaA5}, Duplex: name == "Office", Color: true, DriverDialog: name == "Office"}
		if name == "Office" {
			c.Trays = []string{"auto", "tray-1", "Fach 9"}
			c.Qualities = []goprint.Quality{goprint.QualityDraft, goprint.QualityNormal}
		}
		return c, nil
	}
	propertiesFunc = func(context.Context, goprint.Settings, uintptr) (goprint.Settings, error) {
		t.Fatal("unexpected driver dialog")
		return goprint.Settings{}, nil
	}
	printFunc = func(_ context.Context, _ goprint.Document, s goprint.Settings) (*goprint.Job, error) {
		f.printed = append(f.printed, s)
		return nil, nil
	}
	runAsync = func(f func()) { f() }
	t.Cleanup(func() {
		printersFunc, capsFunc, printFunc, propertiesFunc, runAsync = savedPrinters, savedCaps, savedPrint, savedProps, savedAsync
	})
	return f
}

var testPrinters = []goprint.Printer{
	{Name: "Microsoft Print to PDF"},
	{Name: "Office", Default: true},
	{Name: "Lab"},
}

type result struct {
	called bool
	job    *goprint.Job
	s      goprint.Settings
	err    error
}

func (r *result) done(job *goprint.Job, s goprint.Settings, err error) {
	r.called, r.job, r.s, r.err = true, job, s, err
}

func openTestDialog(t *testing.T, doc goprint.Document, opts PrintDialogOptions) (*printDialog, *result) {
	t.Helper()
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	w.Resize(fyne.NewSize(1000, 700))
	r := &result{}
	d := showPrintDialog(w, doc, opts, r.done)
	return d, r
}

func TestPrintDialogPrints(t *testing.T) {
	f := stubPrinters(t, testPrinters)
	d, r := openTestDialog(t, a4Doc(3), PrintDialogOptions{PrintNow: true, Settings: goprint.Settings{Quality: goprint.QualityHigh}})

	if got, want := d.printer.Options, []string{printerLabel(testPrinters[1]), "Lab"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("printers = %q (file printer must be hidden)", got)
	}
	if d.s.Printer != "Office" || !d.shows(d.duplexItem) || !d.shows(d.colorItem) {
		t.Fatalf("selected %q, duplex shown %v", d.s.Printer, d.shows(d.duplexItem))
	}
	if d.sheet.Image == nil {
		t.Fatal("no preview")
	}

	test.Type(d.copies, "\b2") // "1" → "2"
	d.copies.SetText("2")
	d.ranges.SetText("2-3")
	d.paper.SetSelectedIndex(2) // A5
	d.orientation.SetSelectedIndex(int(goprint.Landscape))
	d.duplex.SetSelectedIndex(int(goprint.DuplexLongEdge))
	if b := d.sheet.Image.Bounds(); b.Dx() <= b.Dy() {
		t.Errorf("landscape preview is %v", b)
	}
	if want := sheetLabel(1, 2, 2); d.pageLabel.Text != want {
		t.Errorf("page label %q", d.pageLabel.Text)
	}
	test.Tap(d.ok)

	if !r.called || r.err != nil || len(f.printed) != 1 {
		t.Fatalf("done %+v, printed %d", r, len(f.printed))
	}
	s := f.printed[0]
	want := goprint.Settings{
		Printer: "Office", Copies: 2, Collate: s.Collate, PageRanges: []goprint.PageRange{{From: 2, To: 3}},
		Media: goprint.MediaA5, Orientation: goprint.Landscape, Duplex: goprint.DuplexLongEdge,
		Quality: goprint.QualityHigh,
	}
	if !reflect.DeepEqual(s, want) || s.Collate == nil || !*s.Collate {
		t.Errorf("printed with %+v\nwant %+v", s, want)
	}
	if !reflect.DeepEqual(r.s, s) {
		t.Errorf("reported %+v", r.s)
	}
}

func TestPrintDialogProperties(t *testing.T) {
	f := stubPrinters(t, testPrinters)
	d, r := openTestDialog(t, a4Doc(3), PrintDialogOptions{PrintNow: true})
	if !d.props.Visible() {
		t.Fatal("no properties button for a printer with a driver dialog")
	}
	d.ranges.SetText("2-3")

	var asked []goprint.Settings
	reply := func(s goprint.Settings) (goprint.Settings, error) {
		s.Media, s.Orientation, s.Duplex, s.Copies = goprint.MediaA5, goprint.Landscape, goprint.DuplexShortEdge, 3
		s.Vendor = map[string]string{goprint.VendorDevMode: "AAAA"}
		return s, nil
	}
	propertiesFunc = func(_ context.Context, s goprint.Settings, _ uintptr) (goprint.Settings, error) {
		asked = append(asked, s)
		if len(asked) == 2 {
			return goprint.Settings{}, goprint.ErrCanceled
		}
		return reply(s)
	}
	test.Tap(d.props)
	if len(asked) != 1 || asked[0].Printer != "Office" {
		t.Fatalf("driver dialog asked with %+v", asked)
	}
	if d.currentMedia() != goprint.MediaA5 || d.orientation.SelectedIndex() != int(goprint.Landscape) ||
		d.duplex.SelectedIndex() != int(goprint.DuplexShortEdge) || d.copies.Text != "3" || d.ranges.Text != "2-3" {
		t.Errorf("controls not updated: paper %v, orientation %d, duplex %d, copies %q, ranges %q",
			d.currentMedia(), d.orientation.SelectedIndex(), d.duplex.SelectedIndex(), d.copies.Text, d.ranges.Text)
	}
	if b := d.sheet.Image.Bounds(); b.Dx() <= b.Dy() {
		t.Errorf("landscape preview is %v", b)
	}

	// Canceling the driver dialog keeps everything.
	test.Tap(d.props)
	if d.orientation.SelectedIndex() != int(goprint.Landscape) || d.s.Vendor[goprint.VendorDevMode] != "AAAA" || d.ok.Disabled() {
		t.Errorf("cancel changed the settings: %+v", d.s)
	}

	test.Tap(d.ok)
	if !r.called || r.err != nil || len(f.printed) != 1 {
		t.Fatalf("done %+v, printed %d", r, len(f.printed))
	}
	s := f.printed[0]
	if s.Vendor[goprint.VendorDevMode] != "AAAA" || s.Media != goprint.MediaA5 || s.Copies != 3 || len(s.PageRanges) != 1 {
		t.Errorf("printed with %+v", s)
	}
}

func TestPrintDialogPropertiesOtherPrinter(t *testing.T) {
	stubPrinters(t, testPrinters)
	dm := map[string]string{goprint.VendorDevMode: "AAAA", "other": "1"}
	d, r := openTestDialog(t, a4Doc(1), PrintDialogOptions{Settings: goprint.Settings{Printer: "Office", Vendor: dm}})
	if d.s.Vendor[goprint.VendorDevMode] != "AAAA" {
		t.Fatal("preset DEVMODE dropped for its own printer")
	}
	d.printer.SetSelected("Lab")
	if d.props.Visible() {
		t.Error("properties button shown for a printer without driver dialog")
	}
	test.Tap(d.ok)
	if _, ok := r.s.Vendor[goprint.VendorDevMode]; ok || r.s.Vendor["other"] != "1" {
		t.Errorf("vendor values after switching printers: %v", r.s.Vendor)
	}
	if len(dm) != 2 {
		t.Errorf("caller's map changed: %v", dm)
	}
}

func TestPrintDialogTrayAndQuality(t *testing.T) {
	stubPrinters(t, testPrinters)
	d, r := openTestDialog(t, a4Doc(1), PrintDialogOptions{Settings: goprint.Settings{Tray: "manual", Quality: goprint.QualityNormal}})
	if !d.shows(d.trayItem) || !d.shows(d.qualityItem) {
		t.Fatal("tray or quality row missing")
	}
	// The preset tray is not listed by the printer but stays selectable.
	want := []string{lang.X("fyneprint.paper.default", "Printer default"), trayLabel("manual"), trayLabel("auto"), trayLabel("tray-1"), "Fach 9"}
	if !reflect.DeepEqual(d.tray.Options, want) || d.currentTray() != "manual" {
		t.Errorf("trays %q, selected %q", d.tray.Options, d.currentTray())
	}
	if len(d.quality.Options) != 3 || d.currentQuality() != goprint.QualityNormal {
		t.Errorf("qualities %q, selected %v", d.quality.Options, d.currentQuality())
	}
	d.tray.SetSelectedIndex(3) // tray-1
	d.quality.SetSelectedIndex(1)
	test.Tap(d.ok)
	if r.s.Tray != "tray-1" || r.s.Quality != goprint.QualityDraft {
		t.Errorf("reported tray %q, quality %v", r.s.Tray, r.s.Quality)
	}

	// A printer without trays and qualities hides the rows and passes the
	// presets through.
	d, r = openTestDialog(t, a4Doc(1), PrintDialogOptions{Settings: goprint.Settings{Printer: "Lab", Tray: "manual", Quality: goprint.QualityHigh}})
	if d.shows(d.trayItem) || d.shows(d.qualityItem) {
		t.Error("tray or quality row shown for Lab")
	}
	test.Tap(d.ok)
	if r.s.Tray != "manual" || r.s.Quality != goprint.QualityHigh {
		t.Errorf("Lab: tray %q, quality %v", r.s.Tray, r.s.Quality)
	}
}

func TestTrayLabel(t *testing.T) {
	// Driver names and unknown keywords stay; "tray-N" is translated.
	for _, in := range []string{"tray-x", "Kassette1", "tray-"} {
		if got := trayLabel(in); got != in {
			t.Errorf("trayLabel(%q) = %q", in, got)
		}
	}
	if got := trayLabel("tray-2"); got == "tray-2" || !strings.Contains(got, "2") {
		t.Errorf("trayLabel(tray-2) = %q", got)
	}
}

func TestPrintDialogPreselects(t *testing.T) {
	stubPrinters(t, testPrinters)
	d, _ := openTestDialog(t, a4Doc(1), PrintDialogOptions{Settings: goprint.Settings{Printer: "Lab"}})
	if d.s.Printer != "Lab" || d.shows(d.duplexItem) || !d.shows(d.colorItem) {
		t.Errorf("selected %q, duplex shown %v", d.s.Printer, d.shows(d.duplexItem))
	}

	// An explicitly requested file printer is listed.
	d, _ = openTestDialog(t, a4Doc(1), PrintDialogOptions{Settings: goprint.Settings{Printer: "Microsoft Print to PDF"}})
	if d.s.Printer != "Microsoft Print to PDF" {
		t.Errorf("selected %q", d.s.Printer)
	}
}

func TestPrintDialogRequirePrinter(t *testing.T) {
	stubPrinters(t, testPrinters)
	_, r := openTestDialog(t, a4Doc(1), PrintDialogOptions{RequirePrinter: true, Settings: goprint.Settings{Printer: "Gone"}})
	if !r.called || !errors.Is(r.err, goprint.ErrPrinterNotFound) {
		t.Errorf("done %+v", r)
	}

	d, r := openTestDialog(t, a4Doc(1), PrintDialogOptions{Settings: goprint.Settings{Printer: "Gone"}})
	if r.called || d.s.Printer != "Office" {
		t.Errorf("without RequirePrinter: selected %q, done %+v", d.s.Printer, r)
	}
}

func TestPrintDialogSettingsOnly(t *testing.T) {
	f := stubPrinters(t, testPrinters)
	d, r := openTestDialog(t, a4Doc(2), PrintDialogOptions{})
	if d.ok.Text != lang.L("OK") {
		t.Errorf("button %q", d.ok.Text)
	}
	test.Tap(d.ok)
	if !r.called || r.err != nil || r.job != nil || r.s.Printer != "Office" || len(f.printed) != 0 {
		t.Errorf("done %+v, printed %d", r, len(f.printed))
	}
}

func TestPrintDialogInvalidInput(t *testing.T) {
	stubPrinters(t, testPrinters)
	d, r := openTestDialog(t, a4Doc(2), PrintDialogOptions{PrintNow: true})
	d.ranges.SetText("5-9")
	if !d.ok.Disabled() || !d.save.Disabled() || d.status.Text == "" {
		t.Errorf("pages beyond the end: ok disabled %v, status %q", d.ok.Disabled(), d.status.Text)
	}
	d.ranges.SetText("2")
	d.copies.SetText("x")
	if !d.ok.Disabled() {
		t.Error("invalid copies accepted")
	}
	d.copies.SetText("1")
	if d.ok.Disabled() || d.status.Text != "" {
		t.Errorf("valid again: ok disabled %v, status %q", d.ok.Disabled(), d.status.Text)
	}
	if r.called {
		t.Errorf("done %+v", r)
	}
}

func TestPrintDialogSave(t *testing.T) {
	f := stubPrinters(t, testPrinters)
	var saved []byte
	d, r := openTestDialog(t, a4Doc(3), PrintDialogOptions{
		SaveLabel: "Als PDF speichern",
		SavePDF: func(rd io.Reader) (err error) {
			saved, err = io.ReadAll(rd)
			return err
		},
	})
	if d.save.Text != "Als PDF speichern" {
		t.Errorf("save label %q", d.save.Text)
	}
	d.ranges.SetText("2")
	test.Tap(d.save)
	if !errors.Is(r.err, ErrSavedAsPDF) || !errors.Is(r.err, goprint.ErrCanceled) || len(f.printed) != 0 {
		t.Fatalf("done %+v", r)
	}
	if got := pdfPages(t, saved); len(got) != 1 {
		t.Errorf("saved %d pages", len(got))
	}
}

func TestPrintDialogCancel(t *testing.T) {
	stubPrinters(t, testPrinters)
	d, r := openTestDialog(t, a4Doc(1), PrintDialogOptions{})
	d.dlg.Hide()
	if !r.called || !errors.Is(r.err, goprint.ErrCanceled) || errors.Is(r.err, ErrSavedAsPDF) {
		t.Errorf("done %+v", r)
	}
}

func TestPrintDialogNoPrinters(t *testing.T) {
	stubPrinters(t, nil)
	d, r := openTestDialog(t, a4Doc(1), PrintDialogOptions{PrintNow: true})
	if !d.ok.Disabled() || d.save.Disabled() || r.called {
		t.Errorf("ok disabled %v, save disabled %v, done %+v", d.ok.Disabled(), d.save.Disabled(), r)
	}
}

func TestPrintDialogBadDocument(t *testing.T) {
	stubPrinters(t, testPrinters)
	_, r := openTestDialog(t, goprint.PDFBytes("bad", []byte("not a pdf")), PrintDialogOptions{})
	if !errors.Is(r.err, goprint.ErrInvalid) {
		t.Errorf("done %+v", r)
	}
}

// TestTranslations checks that every translation file covers exactly the
// keys the code uses.
func TestTranslations(t *testing.T) {
	src, err := os.ReadFile("printdialog.go")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`tr\("([a-z.]+)"|\{"([a-z.]+)", "`).FindAllStringSubmatch(string(src), -1) {
		used["fyneprint."+m[1]+m[2]] = true
	}
	files, err := translations.ReadDir("translations")
	if err != nil || len(files) == 0 {
		t.Fatalf("no translations: %v", err)
	}
	for _, f := range files {
		b, err := translations.ReadFile("translations/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", f.Name(), err)
		}
		for k := range used {
			if m[k] == "" {
				t.Errorf("%s: missing %s", f.Name(), k)
			}
		}
		for k := range m {
			if !used[k] {
				t.Errorf("%s: unused %s", f.Name(), k)
			}
		}
	}
}
