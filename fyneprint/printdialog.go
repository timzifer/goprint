package fyneprint

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
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
	pdf "github.com/timzifer/fyne-pdf"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/core"
)

// PrintDialogOptions configures [ShowPrintDialog].
type PrintDialogOptions struct {
	// Settings are the presets shown in the dialog, the printer included.
	// Settings the dialog has no control for (quality, tray, vendor
	// values, credentials, strict) are passed through unchanged.
	Settings goprint.Settings
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
// Call it on Fyne's UI goroutine, e.g. from a widget callback.
func ShowPrintDialog(w fyne.Window, doc goprint.Document, opts PrintDialogOptions, done func(*goprint.Job, goprint.Settings, error)) {
	showPrintDialog(w, doc, opts, done)
}

func showPrintDialog(w fyne.Window, doc goprint.Document, opts PrintDialogOptions, done func(*goprint.Job, goprint.Settings, error)) *printDialog {
	d := &printDialog{win: w, doc: doc, opts: opts, done: done, s: opts.Settings}
	d.build()
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

	previewPage   int // index into the selected pages
	previewCancel context.CancelFunc
	capsCancel    context.CancelFunc
	finished      bool

	// inputErr (invalid controls) wins over loadErr (printer list or
	// capabilities) in the status line.
	inputErr, loadErr error

	printer, paper, orientation, duplex, color, scaling *widget.Select
	copies, ranges                                      *widget.Entry
	collate                                             *widget.Check
	allPages                                            *widget.RadioGroup
	items                                               []*widget.FormItem
	duplexItem, colorItem                               *widget.FormItem
	form                                                *widget.Form
	sheet                                               *canvas.Image
	pageLabel, status                                   *widget.Label
	prev, next, save, ok                                *widget.Button
}

func (d *printDialog) build() {
	d.printer = widget.NewSelect(nil, func(string) { d.printerChanged() })
	d.printer.PlaceHolder = tr("loading", "Loading printers…")
	d.copies = widget.NewEntry()
	d.copies.SetText(strconv.Itoa(max(d.s.Copies, 1)))
	d.copies.Validator = func(s string) error {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 1 || n > 999 {
			return errors.New(tr("copies.invalid", "Enter a number from 1 to 999"))
		}
		return nil
	}
	d.copies.OnChanged = func(string) { d.changed() }
	d.collate = widget.NewCheck(tr("collate", "Collate"), func(bool) {})
	d.collate.SetChecked(d.s.Collate == nil || *d.s.Collate)

	all, some := tr("pages.all", "All"), tr("pages", "Pages")
	d.ranges = widget.NewEntry()
	d.ranges.SetPlaceHolder("1-3, 5")
	d.ranges.SetText(formatRanges(d.s.PageRanges))
	d.ranges.Validator = func(s string) error {
		_, err := parseRanges(s)
		return err
	}
	d.allPages = widget.NewRadioGroup([]string{all, some}, func(string) { d.changed() })
	d.allPages.Horizontal = true
	d.allPages.Required = true
	if len(d.s.PageRanges) > 0 {
		d.allPages.SetSelected(some)
	} else {
		d.allPages.SetSelected(all)
	}
	d.ranges.OnChanged = func(s string) {
		if strings.TrimSpace(s) != "" && d.allPages.Selected != some {
			d.allPages.SetSelected(some)
		}
		d.changed()
	}

	d.paper = widget.NewSelect(nil, func(string) { d.changed() })
	d.orientation = newEnumSelect(&orientationNames, d.s.Orientation, d.changed)
	d.duplex = newEnumSelect(&duplexNames, d.s.Duplex, d.changed)
	d.color = newEnumSelect(&colorNames, d.s.Color, d.changed)
	d.scaling = newEnumSelect(&scalingNames, d.s.Scaling, d.changed)
	d.duplexItem = widget.NewFormItem(tr("duplex", "Two-sided"), d.duplex)
	d.colorItem = widget.NewFormItem(tr("color", "Color"), d.color)

	d.items = []*widget.FormItem{
		widget.NewFormItem(tr("printer", "Printer"), d.printer),
		widget.NewFormItem(tr("copies", "Copies"), container.NewBorder(nil, nil, nil, d.collate, d.copies)),
		widget.NewFormItem(tr("pages", "Pages"), container.NewBorder(nil, nil, d.allPages, nil, d.ranges)),
		widget.NewFormItem(tr("paper", "Paper size"), d.paper),
		widget.NewFormItem(tr("orientation", "Orientation"), d.orientation),
		d.duplexItem,
		d.colorItem,
		widget.NewFormItem(tr("scaling", "Scaling"), d.scaling),
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
	label := tr("print", "Print")
	if !d.opts.PrintNow {
		label = lang.L("OK")
	}
	d.ok = widget.NewButtonWithIcon(label, theme.ConfirmIcon(), d.confirm)
	d.ok.Importance = widget.HighImportance
	d.ok.Disable()
	cancel := widget.NewButtonWithIcon(lang.L("Cancel"), theme.CancelIcon(), func() { d.finish(nil, goprint.Settings{}, goprint.ErrCanceled) })
	saveLabel := d.opts.SaveLabel
	if saveLabel == "" {
		saveLabel = tr("save", "Save as PDF")
	}
	d.save = widget.NewButtonWithIcon(saveLabel, theme.DocumentSaveIcon(), d.saveAsPDF)
	d.save.Disable()
	left := []fyne.CanvasObject{d.save}
	if d.opts.NoSave {
		left = nil
	}
	buttons := container.NewHBox(append(left, layout.NewSpacer(), cancel, d.ok)...)

	settings := container.NewBorder(nil, d.status, nil, nil, container.NewVScroll(d.form))
	split := container.NewHSplit(preview, settings)
	split.Offset = 0.45
	title := d.doc.Title
	if title == "" {
		title = tr("print", "Print")
	}
	d.dlg = dialog.NewCustomWithoutButtons(title, container.NewBorder(nil, buttons, nil, nil, split), d.win)
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
	for _, p := range all {
		if d.opts.ShowFilePrinters || !isFilePrinter(p.Name) || p.Name == want {
			d.printers = append(d.printers, p)
		}
	}
	if want != "" && !slices.ContainsFunc(d.printers, func(p goprint.Printer) bool { return p.Name == want }) {
		if d.opts.RequirePrinter {
			if err == nil {
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
		names[i] = printerLabel(p)
	}
	d.printer.Options = names
	if len(d.printers) == 0 {
		d.printer.PlaceHolder = tr("noprinters", "No printers")
		d.loadErr = err
		d.showStatus()
		d.printer.Refresh()
		return nil
	}
	d.printer.PlaceHolder = ""
	d.printer.SetSelectedIndex(slices.IndexFunc(d.printers, func(p goprint.Printer) bool { return p.Name == want }))
	return nil
}

func printerLabel(p goprint.Printer) string {
	if p.Default {
		return tr("printer.default", "{{.Name}} (default)", map[string]any{"Name": p.Name})
	}
	return p.Name
}

// isFilePrinter reports printers that write a file instead of paper.
func isFilePrinter(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "print to pdf") || strings.Contains(n, "xps document writer") ||
		strings.Contains(n, "onenote") || n == "pdf" || strings.Contains(n, "cups-pdf")
}

// printerChanged loads the capabilities of the selected printer.
func (d *printDialog) printerChanged() {
	i := d.printer.SelectedIndex()
	if i < 0 || i >= len(d.printers) {
		return
	}
	d.s.Printer = d.printers[i].Name
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
	if cur.Name != "" && !slices.ContainsFunc(d.media, func(m goprint.Media) bool { return m.Name == cur.Name }) {
		d.media = append([]goprint.Media{cur}, d.media...)
	}
	opts := []string{tr("paper.default", "Printer default")}
	sel := 0
	for i, m := range d.media {
		opts = append(opts, mediaLabel(m))
		if m.Name == cur.Name {
			sel = i + 1
		}
	}
	d.paper.Options = opts
	d.paper.SetSelectedIndex(sel)
	d.showItems()
	d.changed()
}

// showItems lists the form rows the printer supports.
func (d *printDialog) showItems() {
	d.form.Items = nil
	for _, it := range d.items {
		if (it == d.duplexItem && !d.caps.Duplex) || (it == d.colorItem && !d.caps.Color) {
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
		return s, fmt.Errorf("%w: copies %q", goprint.ErrInvalid, d.copies.Text)
	}
	s.Copies = n
	if n > 1 {
		c := d.collate.Checked
		s.Collate = &c
	}
	s.PageRanges = nil
	if d.allPages.Selected != tr("pages.all", "All") {
		if s.PageRanges, err = parseRanges(d.ranges.Text); err != nil {
			return s, fmt.Errorf("%w: %v", goprint.ErrInvalid, err)
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
		err = errors.New(tr("pages.none", "No pages selected"))
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
	d.pageLabel.SetText(sheetLabel(d.previewPage+1, len(pages), pages[d.previewPage]+1))

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
func sheetLabel(n, total, page int) string {
	return tr("sheet", "Sheet {{.N}} of {{.Total}} (page {{.Page}})", map[string]any{"N": n, "Total": total, "Page": page})
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

// Choices of the enum selects (translation key, English text), in the
// order of the goprint constants.
var (
	orientationNames = [][2]string{{"orientation.auto", "Automatic"}, {"orientation.portrait", "Portrait"}, {"orientation.landscape", "Landscape"}, {"orientation.rportrait", "Portrait, upside down"}, {"orientation.rlandscape", "Landscape, upside down"}}
	duplexNames      = [][2]string{{"duplex.default", "Printer default"}, {"duplex.none", "One-sided"}, {"duplex.long", "Long edge (book)"}, {"duplex.short", "Short edge (notepad)"}}
	colorNames       = [][2]string{{"color.auto", "Automatic"}, {"color.color", "Color"}, {"color.mono", "Black and white"}}
	scalingNames     = [][2]string{{"scaling.auto", "Shrink to fit"}, {"scaling.fit", "Fit to paper"}, {"scaling.fill", "Fill paper"}, {"scaling.none", "Actual size"}}
)

// tr translates a fyneprint text. Keys are prefixed so that they cannot
// clash with the app's own translations.
func tr(key, fallback string, data ...any) string {
	return lang.X("fyneprint."+key, fallback, data...)
}

// newEnumSelect builds a select over names whose index is the enum value.
func newEnumSelect[T ~int](names *[][2]string, v T, changed func()) *widget.Select {
	opts := make([]string, len(*names))
	for i, n := range *names {
		opts[i] = tr(n[0], n[1])
	}
	s := widget.NewSelect(opts, nil)
	if int(v) >= 0 && int(v) < len(opts) {
		s.SetSelectedIndex(int(v))
	} else {
		s.SetSelectedIndex(0)
	}
	s.OnChanged = func(string) { changed() }
	return s
}
