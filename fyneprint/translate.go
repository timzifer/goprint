package fyneprint

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"text/template"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
)

// Translator supplies the texts of [ShowPrintDialog] for apps with their
// own localization. key is the full key ("fyneprint.copies", and "OK" and
// "Cancel" for those buttons, see [TranslationKeys]), fallback the English
// text and data the template data of texts with placeholders (a
// map[string]any, nil otherwise). An empty result keeps the built-in text
// from Fyne's lang package; [RenderText] expands a template like it.
type Translator func(key, fallback string, data any) string

var (
	translatorMu sync.RWMutex
	translator   Translator
)

// SetTranslator installs t for every print dialog of the app; nil restores
// Fyne's lang lookup. [PrintDialogOptions.Translate] takes precedence. Open
// dialogs pick up a new translator with [RefreshTexts].
func SetTranslator(t Translator) {
	translatorMu.Lock()
	defer translatorMu.Unlock()
	translator = t
}

func currentTranslator() Translator {
	translatorMu.RLock()
	defer translatorMu.RUnlock()
	return translator
}

// texts are all texts of the dialog in English, by full key. The German
// ones are in translations/fyneprint.de.json.
var texts = map[string]string{
	"OK":     "OK",
	"Cancel": "Cancel",

	"fyneprint.print":            "Print",
	"fyneprint.save":             "Save as PDF",
	"fyneprint.save.encrypted":   "This PDF does not allow saving some of its pages",
	"fyneprint.loading":          "Loading printers…",
	"fyneprint.noprinters":       "No printers",
	"fyneprint.printer":          "Printer",
	"fyneprint.printer.default":  "{{.Name}} (default)",
	"fyneprint.properties":       "Properties…",
	"fyneprint.copies":           "Copies",
	"fyneprint.copies.invalid":   "Enter a number from 1 to 999",
	"fyneprint.collate":          "Collate",
	"fyneprint.pages":            "Pages",
	"fyneprint.pages.all":        "All",
	"fyneprint.pages.none":       "No pages selected",
	"fyneprint.pages.invalid":    "Invalid page range {{.Range}}",
	"fyneprint.paper":            "Paper size",
	"fyneprint.paper.default":    "Printer default",
	"fyneprint.orientation":      "Orientation",
	"fyneprint.duplex":           "Two-sided",
	"fyneprint.color":            "Color",
	"fyneprint.quality":          "Quality",
	"fyneprint.quality.draft":    "Draft",
	"fyneprint.quality.normal":   "Normal",
	"fyneprint.quality.high":     "High",
	"fyneprint.tray":             "Paper source",
	"fyneprint.tray.auto":        "Automatic",
	"fyneprint.tray.manual":      "Manual feed",
	"fyneprint.tray.n":           "Tray {{.N}}",
	"fyneprint.scaling":          "Scaling",
	"fyneprint.sheet":            "Sheet {{.N}} of {{.Total}} (page {{.Page}})",
	"fyneprint.orientation.auto": "Automatic",

	"fyneprint.orientation.portrait":   "Portrait",
	"fyneprint.orientation.landscape":  "Landscape",
	"fyneprint.orientation.rportrait":  "Portrait, upside down",
	"fyneprint.orientation.rlandscape": "Landscape, upside down",
	"fyneprint.duplex.default":         "Printer default",
	"fyneprint.duplex.none":            "One-sided",
	"fyneprint.duplex.long":            "Long edge (book)",
	"fyneprint.duplex.short":           "Short edge (notepad)",
	"fyneprint.color.auto":             "Automatic",
	"fyneprint.color.color":            "Color",
	"fyneprint.color.mono":             "Black and white",
	"fyneprint.scaling.auto":           "Shrink to fit",
	"fyneprint.scaling.fit":            "Fit to paper",
	"fyneprint.scaling.fill":           "Fill paper",
	"fyneprint.scaling.none":           "Actual size",
}

// Keys of the enum selects, in the order of the goprint constants.
var (
	orientationKeys = []string{"orientation.auto", "orientation.portrait", "orientation.landscape", "orientation.rportrait", "orientation.rlandscape"}
	duplexKeys      = []string{"duplex.default", "duplex.none", "duplex.long", "duplex.short"}
	colorKeys       = []string{"color.auto", "color.color", "color.mono"}
	qualityKeys     = []string{"paper.default", "quality.draft", "quality.normal", "quality.high"}
	scalingKeys     = []string{"scaling.auto", "scaling.fit", "scaling.fill", "scaling.none"}
)

// TranslationKeys returns the keys of all texts of the print dialog,
// each with its English text. Apps with a [Translator] can check
// in a test that they cover all of them.
func TranslationKeys() map[string]string {
	return maps.Clone(texts)
}

// RenderText expands the placeholders of a text ("{{.Name}}") with data,
// as Fyne's lang package does. A text that is not a valid template is
// returned as is.
func RenderText(text string, data any) string {
	if data == nil || !strings.Contains(text, "{{") {
		return text
	}
	t, err := template.New("").Parse(text)
	if err != nil {
		return text
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return text
	}
	return b.String()
}

// openDialogs are the print dialogs on screen; only touched on the UI
// goroutine.
var openDialogs = map[*printDialog]bool{}

// RefreshTexts redraws the texts of all open print dialogs, e.g. after the
// app switched its language or called [SetTranslator]. Call it on Fyne's
// UI goroutine. The dialog title of documents without a title and Fyne's
// own file dialog keep the texts they opened with.
func RefreshTexts() {
	for _, d := range slices.Collect(maps.Keys(openDialogs)) {
		d.applyTexts()
	}
}

// t returns the text for key ("copies", or "OK"/"Cancel"): from the
// dialog's Translate, the app's translator, or Fyne's lang package.
func (d *printDialog) t(key string, data ...any) string {
	full := key
	if key != "OK" && key != "Cancel" {
		full = "fyneprint." + key
	}
	fallback, ok := texts[full]
	if !ok {
		fyne.LogError("fyneprint: no text for "+full, nil)
		fallback = key
	}
	var v any
	if len(data) > 0 {
		v = data[0]
	}
	for _, f := range []Translator{d.opts.Translate, currentTranslator()} {
		if f != nil {
			if s := f(full, fallback, v); s != "" {
				return s
			}
		}
	}
	if full == "OK" || full == "Cancel" {
		return lang.L(full)
	}
	return lang.X(full, fallback, data...)
}

// textError is an input error shown in the dialog; it matches
// goprint.ErrInvalid.
type textError struct {
	msg string
	err error
}

func (e *textError) Error() string { return e.msg }
func (e *textError) Unwrap() error { return e.err }
