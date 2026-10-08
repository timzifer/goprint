package fyneprint

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/timzifer/cera/pdfedit"
	pdf "github.com/timzifer/fyne-pdf"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/core"
)

// PrintDialogOptions configures [ShowPrintDialog].
type PrintDialogOptions struct {
	// Settings are the presets shown in the dialog, the printer included.
	// Settings the dialog has no control for (vendor values, credentials,
	// strict; tray and quality where the printer does not list them) are
	// passed through unchanged, except
	// that the driver's settings (Vendor[goprint.VendorDevMode]) are
	// dropped when the user picks another printer.
	Settings goprint.Settings
	// Translate, if set, supplies the dialog's texts, before the app-wide
	// [SetTranslator] and Fyne's lang package; see [Translator].
	Translate Translator
	// RequirePrinter makes the dialog fail with goprint.ErrPrinterNotFound
	// if Settings.Printer does not exist, instead of falling back to the
	// default printer.
	RequirePrinter bool
	// PrintNow prints after confirmation. If false, the dialog only
	// returns the chosen settings and a nil Job.
	PrintNow bool

	// SaveLabel is the text of the button that saves the document as PDF;
	// empty means "Save as PDF".
	SaveLabel string
	// NoSave hides that button.
	NoSave bool
	// SavePDF receives the PDF to save. Nil shows Fyne's file save dialog.
	SavePDF func(r io.Reader) error
	// ShowFilePrinters lists printers that write files ("Microsoft Print to
	// PDF", "Microsoft XPS Document Writer", CUPS-PDF, ...). They are hidden
	// by default: printing headless to them cannot ask for a file name, and
	// the save button covers the use case.
	ShowFilePrinters bool
	// NoFileOutput keeps the dialog away from the file system: printers
	// that write files are never listed (not even as preset; with
	// RequirePrinter that is goprint.ErrFileOutput), and the save button
	// is hidden unless SavePDF hands the PDF to the app.
	NoFileOutput bool
}

// ErrSavedAsPDF is reported when the user saved the document as PDF
// instead of printing. It matches goprint.ErrCanceled too, so callers that
// only check for cancellation treat it as "nothing printed".
var ErrSavedAsPDF = fmt.Errorf("fyneprint: saved as PDF (%w)", goprint.ErrCanceled)

//go:embed translations
var translations embed.FS

func init() {
	if err := lang.AddTranslationsFS(translations, "translations"); err != nil {
		fyne.LogError("fyneprint: loading translations", err)
	}
}

// Replaced in tests, so that they never reach a real printer.
var (
	printersFunc = goprint.Printers
	capsFunc     = goprint.GetCapabilities
	printFunc    = goprint.Print
	// propertiesFunc shows the driver's dialog.
	propertiesFunc = goprint.PrinterProperties
	// runAsync runs blocking work off the UI goroutine.
	runAsync = func(f func()) { go f() }
)

// ShowPrintDialog shows a print dialog drawn by Fyne itself, with a
// preview of the sheets as they will come out of the printer. Unlike
// [ShowDialog] it does not use the platform's dialog, so it works the same
// everywhere and can always preselect the printer. On confirmation it
// prints through goprint's headless [goprint.Print] (if opts.PrintNow) and
// calls done on the UI goroutine with the job and the chosen settings.
//
// The save button writes the selected pages as PDF and reports
// [ErrSavedAsPDF]. Cancel reports goprint.ErrCanceled. done may be nil.
//
// For printers with a driver dialog (Windows, see
// [goprint.PrinterProperties]) a "Properties…" button next to the printer
// opens it. What the user chooses there is printed along, also driver
// options the dialog itself has no control for.
//
// Call it on Fyne's UI goroutine, e.g. from a widget callback.
func ShowPrintDialog(w fyne.Window, doc goprint.Document, opts PrintDialogOptions, done func(*goprint.Job, goprint.Settings, error)) {
	showPrintDialog(w, doc, opts, done)
}

func showPrintDialog(w fyne.Window, doc goprint.Document, opts PrintDialogOptions, done func(*goprint.Job, goprint.Settings, error)) *printDialog {
	d := &printDialog{win: w, doc: doc, opts: opts, done: done, s: opts.Settings}
	d.build()
	openDialogs[d] = true
	d.dlg.Show()
	d.load()
	return d
}

type printDialog struct {
	win  fyne.Window
	doc  goprint.Document
	opts PrintDialogOptions
	done func(*goprint.Job, goprint.Settings, error)
	dlg  dialog.Dialog

	// s holds the settings as edited so far.
	s goprint.Settings

	data     []byte
	src      *pdf.Source
	renderer pageRenderer
	sizes    []pageSize
	printers []goprint.Printer // shown in the list
	caps     goprint.Capabilities
	media    []goprint.Media // media offered for the current printer
	trays    []string        // trays listed in d.tray
	quals    []goprint.Quality

	previewPage   int // index into the selected pages
	previewCancel context.CancelFunc
	capsCancel    context.CancelFunc
	finished      bool

	// inputErr (invalid controls) wins over loadErr (printer list or
	// capabilities) in the status line.
	inputErr, loadErr error

	printer, paper, orientation, duplex, color, scaling *widget.Select
	tray, quality                                       *widget.Select
	copies, ranges                                      *widget.Entry
	collate                                             *widget.Check
	allPages                                            *widget.RadioGroup
	items                                               []*widget.FormItem
	duplexItem, colorItem, trayItem, qualityItem        *widget.FormItem
	form                                                *widget.Form
	buttons                                             *fyne.Container
	sheet                                               *canvas.Image
	pageLabel, status                                   *widget.Label
	prev, next, save, ok, props, cancel                 *widget.Button
	printerItem, copiesItem, pagesItem, paperItem       *widget.FormItem
	orientationItem, scalingItem                        *widget.FormItem
	printersLoaded                                      bool
}

func (d *printDialog) build() {
	d.printer = widget.NewSelect(nil, func(string) { d.printerChanged() })
	d.props = widget.NewButton("", d.showProperties)
	d.props.Hide()
	d.copies = widget.NewEntry()
	d.copies.SetText(strconv.Itoa(max(d.s.Copies, 1)))
	d.copies.Validator = func(s string) error {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 1 || n > 999 {
			return errors.New(d.t("copies.invalid"))
		}
		return nil
	}
	d.copies.OnChanged = func(string) { d.changed() }
	d.collate = widget.NewCheck("", func(bool) {})
	d.collate.SetChecked(d.s.Collate == nil || *d.s.Collate)

	d.ranges = widget.NewEntry()
	d.ranges.SetPlaceHolder("1-3, 5")
	d.ranges.SetText(formatRanges(d.s.PageRanges))
	d.ranges.Validator = func(s string) error {
		_, err := d.parseRanges(s)
		return err
	}
	// Options are set by applyTexts; the selection is kept by index.
	d.allPages = widget.NewRadioGroup([]string{"", ""}, func(string) { d.changed() })
	d.allPages.Horizontal = true
	d.allPages.Required = true
	d.ranges.OnChanged = func(s string) {
		if strings.TrimSpace(s) != "" && d.allPagesSelected() {
			d.allPages.SetSelected(d.allPages.Options[1])
		}
		d.changed()
	}

	d.paper = widget.NewSelect(nil, func(string) { d.changed() })
	d.orientation = newEnumSelect(d.changed)
	d.duplex = newEnumSelect(d.changed)
	d.color = newEnumSelect(d.changed)
	d.scaling = newEnumSelect(d.changed)
	d.tray = widget.NewSelect(nil, func(string) { d.changed() })
	d.quality = widget.NewSelect(nil, func(string) { d.changed() })
	d.printerItem = widget.NewFormItem("", container.NewBorder(nil, nil, nil, d.props, d.printer))
	d.copiesItem = widget.NewFormItem("", container.NewBorder(nil, nil, nil, d.collate, d.copies))
	d.pagesItem = widget.NewFormItem("", container.NewBorder(nil, nil, d.allPages, nil, d.ranges))
	d.paperItem = widget.NewFormItem("", d.paper)
	d.orientationItem = widget.NewFormItem("", d.orientation)
	d.duplexItem = widget.NewFormItem("", d.duplex)
	d.colorItem = widget.NewFormItem("", d.color)
	d.qualityItem = widget.NewFormItem("", d.quality)
	d.trayItem = widget.NewFormItem("", d.tray)
	d.scalingItem = widget.NewFormItem("", d.scaling)

	d.items = []*widget.FormItem{
		d.printerItem, d.copiesItem, d.pagesItem, d.paperItem, d.orientationItem,
		d.duplexItem, d.colorItem, d.qualityItem, d.trayItem, d.scalingItem,
	}
	d.form = widget.NewForm()
	d.showItems()

	d.sheet = canvas.NewImageFromImage(nil)
	d.sheet.FillMode = canvas.ImageFillContain
	d.sheet.SetMinSize(fyne.NewSize(300, 400))
	d.pageLabel = widget.NewLabel("")
	d.pageLabel.Alignment = fyne.TextAlignCenter
	d.prev = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { d.turn(-1) })
	d.next = widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() { d.turn(1) })
	preview := container.NewBorder(nil, container.NewBorder(nil, nil, d.prev, d.next, d.pageLabel), nil, nil,
		container.NewStack(canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground)), container.NewPadded(d.sheet)))

	d.status = widget.NewLabel("")
	d.status.Wrapping = fyne.TextWrapWord
	d.ok = widget.NewButtonWithIcon("", theme.ConfirmIcon(), d.confirm)
	d.ok.Importance = widget.HighImportance
	d.ok.Disable()
	d.cancel = widget.NewButtonWithIcon("", theme.CancelIcon(), func() { d.finish(nil, goprint.Settings{}, goprint.ErrCanceled) })
	d.save = widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), d.saveAsPDF)
	d.save.Disable()
	left := []fyne.CanvasObject{d.save}
	if d.opts.NoSave || (d.opts.NoFileOutput && d.opts.SavePDF == nil) {
		left = nil
	}
	d.buttons = container.NewHBox(append(left, layout.NewSpacer(), d.cancel, d.ok)...)
	d.applyTexts()

	settings := container.NewBorder(nil, d.status, nil, nil, container.NewVScroll(d.form))
	split := container.NewHSplit(preview, settings)
	split.Offset = 0.45
	title := d.doc.Title
	if title == "" {
		title = d.t("print")
	}
	d.dlg = dialog.NewCustomWithoutButtons(title, container.NewBorder(nil, d.buttons, nil, nil, split), d.win)
	d.dlg.SetOnClosed(func() { d.finish(nil, goprint.Settings{}, goprint.ErrCanceled) })
	if d.win != nil {
		sz := d.win.Canvas().Size()
		d.dlg.Resize(fyne.NewSize(max(sz.Width*0.9, 760), max(sz.Height*0.9, 520)))
	}
}

// load reads the document and the printer list in the background.
func (d *printDialog) load() {
	runAsync(func() {
		data, src, sizes, docErr := openDocument(d.doc)
		printers, prErr := printersFunc(context.Background())
		fyne.Do(func() {
			if docErr != nil {
				d.finish(nil, goprint.Settings{}, docErr)
				return
			}
			d.data, d.src, d.renderer, d.sizes = data, src, src, sizes
			d.save.Enable()
			if err := d.setPrinters(printers, prErr); err != nil {
				d.finish(nil, goprint.Settings{}, err)
				return
			}
			d.changed()
		})
	})
}

func openDocument(doc goprint.Document) ([]byte, *pdf.Source, []pageSize, error) {
	data, err := documentPDF(doc)
	if err != nil {
		return nil, nil, nil, err
	}
	src, err := pdf.OpenSource(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", goprint.ErrInvalid, err)
	}
	sizes := make([]pageSize, src.PageCount())
	for i := range sizes {
		b, err := src.Bound(i)
		if err != nil {
			src.Close()
			return nil, nil, nil, fmt.Errorf("%w: %v", goprint.ErrInvalid, err)
		}
		sizes[i] = pageSize{float64(b.Dx()), float64(b.Dy())}
	}
	return data, src, sizes, nil
}

// setPrinters fills the printer list and selects the preset printer, the
// default printer or the first one.
func (d *printDialog) setPrinters(all []goprint.Printer, err error) error {
	want := d.s.Printer
	refused := false
	for _, p := range all {
		switch {
		case p.ToFile && d.opts.NoFileOutput:
			refused = refused || p.Name == want
		case !p.ToFile || d.opts.ShowFilePrinters || p.Name == want:
			d.printers = append(d.printers, p)
		}
	}
	if want != "" && !slices.ContainsFunc(d.printers, func(p goprint.Printer) bool { return p.Name == want }) {
		if d.opts.RequirePrinter {
			switch {
			case refused:
				err = goprint.ErrFileOutput
			case err == nil:
				err = goprint.ErrPrinterNotFound
			}
			return fmt.Errorf("%w: %q", err, want)
		}
		want = ""
	}
	if want == "" {
		if i := slices.IndexFunc(d.printers, func(p goprint.Printer) bool { return p.Default }); i >= 0 {
			want = d.printers[i].Name
		} else if len(d.printers) > 0 {
			want = d.printers[0].Name
		}
	}
	names := make([]string, len(d.printers))
	for i, p := range d.printers {
		names[i] = d.printerLabel(p)
	}
	d.printer.Options = names
	if len(d.printers) == 0 {
		d.printersLoaded = true
		d.printer.PlaceHolder = d.t("noprinters")
		d.loadErr = err
		d.showStatus()
		d.printer.Refresh()
		return nil
	}
	d.printersLoaded = true
	d.printer.PlaceHolder = ""
	d.printer.SetSelectedIndex(slices.IndexFunc(d.printers, func(p goprint.Printer) bool { return p.Name == want }))
	return nil
}

func (d *printDialog) printerLabel(p goprint.Printer) string {
	if p.Default {
		return d.t("printer.default", map[string]any{"Name": p.Name})
	}
	return p.Name
}

// printerChanged loads the capabilities of the selected printer.
func (d *printDialog) printerChanged() {
	i := d.printer.SelectedIndex()
	if i < 0 || i >= len(d.printers) {
		return
	}
	if name := d.printers[i].Name; name != d.s.Printer {
		d.s.Printer = name
		// Driver settings belong to the printer they were made for.
		if _, ok := d.s.Vendor[goprint.VendorDevMode]; ok {
			d.s.Vendor = maps.Clone(d.s.Vendor)
			delete(d.s.Vendor, goprint.VendorDevMode)
		}
	}
	d.props.Hide()
	d.changed()
	if d.capsCancel != nil {
		d.capsCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.capsCancel = cancel
	name := d.s.Printer
	runAsync(func() {
		caps, err := capsFunc(ctx, name)
		if ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			d.loadErr = err
			d.setCaps(caps)
		})
	})
}

func (d *printDialog) setCaps(caps goprint.Capabilities) {
	d.caps = caps
	d.media = slices.Clone(caps.Media)
	if len(d.media) == 0 {
		d.media = []goprint.Media{goprint.MediaA4, goprint.MediaLetter, goprint.MediaA3, goprint.MediaA5, goprint.MediaLegal}
	}
	cur := d.currentMedia()
	if cur.Name == "" {
		cur = d.s.Media
	}
	d.selectMedia(cur)
	d.selectTray(d.currentTray())
	d.selectQuality(d.currentQuality())
	if caps.DriverDialog {
		d.props.Show()
	}
	d.showItems()
	d.changed()
}

// selectMedia selects cur in the paper list, adding it if the printer
// does not list it; an empty cur selects the printer default.
func (d *printDialog) selectMedia(cur goprint.Media) {
	if cur.Name != "" && !slices.ContainsFunc(d.media, func(m goprint.Media) bool { return m.Name == cur.Name }) {
		d.media = append([]goprint.Media{cur}, d.media...)
	}
	opts := []string{d.t("paper.default")}
	sel := 0
	for i, m := range d.media {
		opts = append(opts, mediaLabel(m))
		if m.Name == cur.Name {
			sel = i + 1
		}
	}
	d.paper.Options = opts
	d.paper.SetSelectedIndex(sel)
}

// selectTray lists the printer's trays and selects cur, else the preset.
// A preset the printer does not list stays selectable; a tray chosen for
// another printer does not.
func (d *printDialog) selectTray(cur string) {
	d.trays = slices.Clone(d.caps.Trays)
	if cur == "" || !slices.Contains(d.trays, cur) {
		cur = d.s.Tray
	}
	if cur != "" && len(d.trays) > 0 && !slices.Contains(d.trays, cur) {
		d.trays = append([]string{cur}, d.trays...)
	}
	opts := []string{d.t("paper.default")}
	for _, t := range d.trays {
		opts = append(opts, d.trayLabel(t))
	}
	d.tray.Options = opts
	d.tray.SetSelectedIndex(slices.Index(d.trays, cur) + 1)
}

func (d *printDialog) currentTray() string {
	if i := d.tray.SelectedIndex(); i > 0 && i <= len(d.trays) {
		return d.trays[i-1]
	}
	return ""
}

// selectQuality works like selectTray.
func (d *printDialog) selectQuality(cur goprint.Quality) {
	d.quals = slices.Clone(d.caps.Qualities)
	if cur == goprint.QualityDefault || !slices.Contains(d.quals, cur) {
		cur = d.s.Quality
	}
	if cur != goprint.QualityDefault && len(d.quals) > 0 && !slices.Contains(d.quals, cur) {
		d.quals = append(d.quals, cur)
		slices.Sort(d.quals)
	}
	opts := []string{d.t("paper.default")}
	for _, q := range d.quals {
		if int(q) < len(qualityKeys) {
			opts = append(opts, d.t(qualityKeys[q]))
		} else {
			opts = append(opts, q.String())
		}
	}
	d.quality.Options = opts
	d.quality.SetSelectedIndex(slices.Index(d.quals, cur) + 1)
}

func (d *printDialog) currentQuality() goprint.Quality {
	if i := d.quality.SelectedIndex(); i > 0 && i <= len(d.quals) {
		return d.quals[i-1]
	}
	return goprint.QualityDefault
}

// trayLabel names IPP tray keywords; driver bin names are shown as they
// are.
func (d *printDialog) trayLabel(t string) string {
	switch t {
	case "auto":
		return d.t("tray.auto")
	case "manual":
		return d.t("tray.manual")
	}
	if n, ok := strings.CutPrefix(t, "tray-"); ok {
		if _, err := strconv.Atoi(n); err == nil {
			return d.t("tray.n", map[string]any{"N": n})
		}
	}
	return t
}

// showProperties opens the driver's dialog with the current settings and
// takes over what the user chose there.
func (d *printDialog) showProperties() {
	s, err := d.settings()
	if err != nil {
		d.status.SetText(err.Error())
		return
	}
	d.props.Disable()
	d.ok.Disable()
	owner := Owner(d.win)
	runAsync(func() {
		got, err := propertiesFunc(context.Background(), s, owner)
		fyne.Do(func() {
			d.props.Enable()
			if d.finished || d.s.Printer != s.Printer {
				return
			}
			switch {
			case errors.Is(err, goprint.ErrCanceled):
			case err != nil:
				d.loadErr = err
			default:
				d.apply(got)
			}
			d.changed()
		})
	})
}

// apply shows s, as read back from the driver's dialog, in the controls.
// Page ranges and scaling stay: the driver does not know them.
func (d *printDialog) apply(s goprint.Settings) {
	s.Printer, s.PageRanges, s.Scaling = d.s.Printer, d.s.PageRanges, d.s.Scaling
	d.s = s
	d.copies.SetText(strconv.Itoa(max(s.Copies, 1)))
	if s.Collate != nil {
		d.collate.SetChecked(*s.Collate)
	}
	if s.Media.Name != "" {
		d.selectMedia(s.Media)
	}
	d.orientation.SetSelectedIndex(int(s.Orientation))
	d.duplex.SetSelectedIndex(int(s.Duplex))
	d.color.SetSelectedIndex(int(s.Color))
	d.selectTray(s.Tray)
	d.selectQuality(s.Quality)
}

// showItems lists the form rows the printer supports.
func (d *printDialog) showItems() {
	d.form.Items = nil
	for _, it := range d.items {
		if (it == d.duplexItem && !d.caps.Duplex) || (it == d.colorItem && !d.caps.Color) ||
			(it == d.trayItem && len(d.trays) == 0) || (it == d.qualityItem && len(d.quals) == 0) {
			continue
		}
		d.form.Items = append(d.form.Items, it)
	}
	d.form.Refresh()
}

// shows reports whether the form row it is listed.
func (d *printDialog) shows(it *widget.FormItem) bool {
	return slices.Contains(d.form.Items, it)
}

func (d *printDialog) currentMedia() goprint.Media {
	if i := d.paper.SelectedIndex(); i > 0 && i <= len(d.media) {
		return d.media[i-1]
	}
	return goprint.Media{}
}

// mediaLabel turns "iso_a4_210x297mm" into "A4 (210 × 297 mm)".
func mediaLabel(m goprint.Media) string {
	parts := strings.Split(m.Name, "_")
	if len(parts) < 3 {
		return m.String()
	}
	name := parts[1]
	switch parts[0] {
	case "iso", "jis", "jpn", "prc", "roc":
		name = strings.ToUpper(name)
	default:
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	dim := parts[len(parts)-1]
	unit := dim[len(dim)-2:]
	w, h, _ := strings.Cut(dim[:len(dim)-2], "x")
	return fmt.Sprintf("%s (%s × %s %s)", name, w, h, unit)
}

// settings reads the controls into goprint.Settings.
func (d *printDialog) settings() (goprint.Settings, error) {
	s := d.s
	n, err := strconv.Atoi(strings.TrimSpace(d.copies.Text))
	if err != nil || n < 1 {
		return s, &textError{d.t("copies.invalid"), goprint.ErrInvalid}
	}
	s.Copies = n
	if n > 1 {
		c := d.collate.Checked
		s.Collate = &c
	}
	s.PageRanges = nil
	if !d.allPagesSelected() {
		if s.PageRanges, err = d.parseRanges(d.ranges.Text); err != nil {
			return s, err
		}
	}
	s.Media = d.currentMedia()
	s.Orientation = goprint.Orientation(d.orientation.SelectedIndex())
	if d.caps.Duplex {
		s.Duplex = goprint.Duplex(d.duplex.SelectedIndex())
	}
	if d.caps.Color {
		s.Color = goprint.ColorMode(d.color.SelectedIndex())
	}
	if d.shows(d.trayItem) {
		s.Tray = d.currentTray()
	}
	if d.shows(d.qualityItem) {
		s.Quality = d.currentQuality()
	}
	s.Scaling = goprint.Scaling(d.scaling.SelectedIndex())
	return s, nil
}

// selectedPages returns the 0-based pages the settings print.
func (d *printDialog) selectedPages(s goprint.Settings) []int {
	pages, _ := core.SelectPages(toCoreRanges(s.PageRanges), len(d.sizes))
	return pages
}

// changed validates the controls and refreshes the preview.
func (d *printDialog) changed() {
	if d.sizes == nil {
		return
	}
	s, err := d.settings()
	d.collate.Enable()
	if s.Copies < 2 {
		d.collate.Disable()
	}
	pages := d.selectedPages(s)
	if err == nil && len(pages) == 0 {
		err = errors.New(d.t("pages.none"))
	}
	d.inputErr = err
	d.showStatus()
	if err != nil || d.printer.SelectedIndex() < 0 {
		d.ok.Disable()
	} else {
		d.ok.Enable()
	}
	if err != nil || len(pages) == 0 {
		d.save.Disable()
		return
	}
	d.save.Enable()
	d.previewPage = min(d.previewPage, len(pages)-1)
	d.renderPreview(s, pages)
}

func (d *printDialog) showStatus() {
	switch {
	case d.inputErr != nil:
		d.status.SetText(d.inputErr.Error())
	case d.loadErr != nil:
		d.status.SetText(d.loadErr.Error())
	default:
		d.status.SetText("")
	}
}

func (d *printDialog) turn(delta int) {
	d.previewPage += delta
	d.changed()
}

func (d *printDialog) renderPreview(s goprint.Settings, pages []int) {
	d.prev.Disable()
	d.next.Disable()
	if d.previewPage > 0 {
		d.prev.Enable()
	}
	if d.previewPage < len(pages)-1 {
		d.next.Enable()
	}
	d.pageLabel.SetText(d.sheetLabel(d.previewPage+1, len(pages), pages[d.previewPage]+1))

	if d.previewCancel != nil {
		d.previewCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.previewCancel = cancel
	page := pages[d.previewPage]
	sz := d.sizes[page]
	paper := paperFor(sz, s.Media, s.Orientation)
	r := d.renderer
	runAsync(func() {
		img, err := renderSheet(ctx, r, page, sz, paper, s.Scaling, 1000)
		if ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if err != nil {
				d.status.SetText(err.Error())
				return
			}
			d.sheet.Image = img
			d.sheet.Refresh()
		})
	})
}

// sheetLabel names sheet n of total, which shows page (1-based).
func (d *printDialog) sheetLabel(n, total, page int) string {
	return d.t("sheet", map[string]any{"N": n, "Total": total, "Page": page})
}

func (d *printDialog) confirm() {
	s, err := d.settings()
	if err != nil {
		d.status.SetText(err.Error())
		return
	}
	if !d.opts.PrintNow {
		d.finish(nil, s, nil)
		return
	}
	d.ok.Disable()
	doc := d.doc
	runAsync(func() {
		job, err := printFunc(context.Background(), doc, s)
		fyne.Do(func() { d.finish(job, s, err) })
	})
}

func (d *printDialog) saveAsPDF() {
	s, err := d.settings()
	if err != nil {
		d.status.SetText(err.Error())
		return
	}
	d.save.Disable()
	data, sizes := d.data, d.sizes
	runAsync(func() {
		out, err := savedPDF(context.Background(), data, sizes, s)
		fyne.Do(func() {
			d.save.Enable()
			if errors.Is(err, pdfedit.ErrEncrypted) {
				d.status.SetText(d.t("save.encrypted"))
				return
			}
			if err != nil {
				d.status.SetText(err.Error())
				return
			}
			if d.opts.SavePDF != nil {
				if err := d.opts.SavePDF(bytes.NewReader(out)); err != nil {
					d.status.SetText(err.Error())
					return
				}
				d.finish(nil, s, ErrSavedAsPDF)
				return
			}
			d.showSaveDialog(out, s)
		})
	})
}

func (d *printDialog) showSaveDialog(out []byte, s goprint.Settings) {
	fd := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err == nil && wc == nil {
			return // canceled: back to the print dialog
		}
		if err == nil {
			_, err = wc.Write(out)
			if cerr := wc.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			d.status.SetText(err.Error())
			return
		}
		d.finish(nil, s, ErrSavedAsPDF)
	}, d.win)
	name := d.doc.Title
	if name == "" {
		name = "document"
	}
	fd.SetFileName(name + ".pdf")
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".pdf"}))
	if d.win != nil {
		sz := d.win.Canvas().Size()
		fd.Resize(fyne.NewSize(sz.Width*0.8, sz.Height*0.8))
	}
	fd.Show()
}

// finish closes the dialog and reports the result once. Hiding the dialog
// calls finish again through OnClosed; that call does nothing.
func (d *printDialog) finish(job *goprint.Job, s goprint.Settings, err error) {
	if d.finished {
		return
	}
	d.finished = true
	delete(openDialogs, d)
	if d.previewCancel != nil {
		d.previewCancel()
	}
	if d.capsCancel != nil {
		d.capsCancel()
	}
	d.dlg.Hide()
	if src := d.src; src != nil {
		// Close waits for a running render (it is canceled above); keep that
		// off the UI goroutine.
		runAsync(func() { src.Close() })
	}
	if d.done != nil {
		d.done(job, s, err)
	}
}

// applyTexts sets every text of the dialog, at build time and from
// RefreshTexts. Selections are kept by index.
func (d *printDialog) applyTexts() {
	first := len(d.orientation.Options) == 0
	pick := func(sel *widget.Select, opts []string, initial int) {
		i := sel.SelectedIndex()
		if first {
			i = initial
		}
		sel.Options = opts
		if i < 0 || i >= len(opts) {
			i = 0
		}
		sel.SetSelectedIndex(i)
		sel.Refresh()
	}
	enum := func(sel *widget.Select, keys []string, initial int) {
		opts := make([]string, len(keys))
		for i, k := range keys {
			opts[i] = d.t(k)
		}
		pick(sel, opts, initial)
	}
	enum(d.orientation, orientationKeys, int(d.s.Orientation))
	enum(d.duplex, duplexKeys, int(d.s.Duplex))
	enum(d.color, colorKeys, int(d.s.Color))
	enum(d.scaling, scalingKeys, int(d.s.Scaling))

	some := 0
	if first && len(d.s.PageRanges) > 0 || !first && !d.allPagesSelected() {
		some = 1
	}
	d.allPages.Options = []string{d.t("pages.all"), d.t("pages")}
	d.allPages.SetSelected(d.allPages.Options[some])
	d.allPages.Refresh()

	for it, key := range map[*widget.FormItem]string{
		d.printerItem: "printer", d.copiesItem: "copies", d.pagesItem: "pages", d.paperItem: "paper",
		d.orientationItem: "orientation", d.duplexItem: "duplex", d.colorItem: "color",
		d.qualityItem: "quality", d.trayItem: "tray", d.scalingItem: "scaling",
	} {
		it.Text = d.t(key)
	}
	d.form.Refresh()

	switch {
	case !d.printersLoaded:
		d.printer.PlaceHolder = d.t("loading")
	case len(d.printers) == 0:
		d.printer.PlaceHolder = d.t("noprinters")
	default:
		i := d.printer.SelectedIndex()
		opts := make([]string, len(d.printers))
		for i, p := range d.printers {
			opts[i] = d.printerLabel(p)
		}
		d.printer.Options = opts
		if i >= 0 {
			// Same printer, new label: no reload of its capabilities.
			changed := d.printer.OnChanged
			d.printer.OnChanged = nil
			d.printer.SetSelectedIndex(i)
			d.printer.OnChanged = changed
		}
	}
	d.printer.Refresh()
	if !first {
		d.selectMedia(d.currentMedia())
		d.selectTray(d.currentTray())
		d.selectQuality(d.currentQuality())
	}

	d.props.SetText(d.t("properties"))
	d.collate.Text = d.t("collate")
	d.collate.Refresh()
	if d.opts.PrintNow {
		d.ok.SetText(d.t("print"))
	} else {
		d.ok.SetText(d.t("OK"))
	}
	d.cancel.SetText(d.t("Cancel"))
	if d.opts.SaveLabel != "" {
		d.save.SetText(d.opts.SaveLabel)
	} else {
		d.save.SetText(d.t("save"))
	}
	if !first {
		d.changed() // status line and sheet label
	}
}

// allPagesSelected reports whether "All" is chosen in the pages radio.
func (d *printDialog) allPagesSelected() bool {
	return d.allPages.Selected == "" || d.allPages.Selected == d.allPages.Options[0]
}

// parseRanges is parseRanges with an error text for the dialog.
func (d *printDialog) parseRanges(s string) ([]goprint.PageRange, error) {
	rs, err := parseRanges(s)
	var re *rangeError
	if errors.As(err, &re) {
		return nil, &textError{d.t("pages.invalid", map[string]any{"Range": re.part}), goprint.ErrInvalid}
	}
	return rs, err
}

// newEnumSelect builds a select whose index is an enum value; options and
// the initial selection are set by applyTexts.
func newEnumSelect(changed func()) *widget.Select {
	return widget.NewSelect(nil, func(string) { changed() })
}
