// Package macprint shows the macOS print panel and runs PDFKit print
// operations. It talks to AppKit, PDFKit and PrintCore through the
// Objective-C runtime with purego, without cgo.
//
// The types in this file are plain Go and build on every platform so that
// goprint can unit-test its settings mapping anywhere; the implementation
// is darwin-only.
package macprint

// Orientation values of NSPrintInfo (NSPaperOrientation).
const (
	OrientationPortrait  = 0
	OrientationLandscape = 1
)

// PMDuplexMode values of PrintCore.
const (
	DuplexNone      = 1 // kPMDuplexNone
	DuplexLongEdge  = 2 // kPMDuplexNoTumble
	DuplexShortEdge = 3 // kPMDuplexTumble
)

// PDFPrintScalingMode values of PDFKit.
const (
	ScaleNone      = 0 // kPDFPrintPageScaleNone
	ScaleToFit     = 1 // kPDFPrintPageScaleToFit
	ScaleDownToFit = 2 // kPDFPrintPageScaleDownToFit
)

// Tri-state values for Options.Collate and Options.Color.
const (
	Default = 0
	Yes     = 1
	No      = 2
)

// Job dispositions (NSPrintJobDispositionValue) as reported in
// Result.Disposition.
const (
	DispositionSpool   = "NSPrintSpoolJob"
	DispositionPreview = "NSPrintPreviewJob"
	DispositionSave    = "NSPrintSaveJob"
	DispositionCancel  = "NSPrintCancelJob"
)

// Options are the presets of a print operation in AppKit/PrintCore terms.
// Zero values leave the NSPrintInfo default untouched.
type Options struct {
	// Title is the job title of the print operation.
	Title string
	// Printer is the queue name for [NSPrinter printerWithName:].
	Printer string
	// PaperWidth and PaperHeight are the portrait paper size in points.
	PaperWidth, PaperHeight float64
	// Orientation is OrientationPortrait or OrientationLandscape; it is
	// applied only if SetOrientation is true.
	Orientation    int
	SetOrientation bool
	// Copies is the number of copies; 0 keeps the default.
	Copies int
	// FirstPage and LastPage (1-based) select a single page range. 0/0
	// prints all pages; LastPage 0 means "to the end".
	FirstPage, LastPage int
	// Collate is Default, Yes or No.
	Collate int
	// Duplex is a PMDuplexMode; 0 keeps the default.
	Duplex int
	// Color is Default, Yes (color) or No (grayscale).
	Color int
	// Scaling is a PDFPrintScalingMode for the PDFKit print operation.
	Scaling int
	// AutoRotate lets PDFKit rotate pages to match the paper orientation.
	AutoRotate bool
	// Accept, if set, vets what the user confirmed in the panel before
	// anything is printed or saved. With it, Dialog shows the panel on its
	// own (without preview) and runs the print operation afterwards.
	Accept func(Result) error
	// Extra are additional print settings (PMPrintSettingsSetValue). The
	// macOS print system hands them to CUPS as job options, so IPP
	// attribute names work.
	Extra map[string]string
}

// Result is what NSPrintInfo and PMPrintSettings report after the print
// panel or operation ran.
type Result struct {
	// Printer is the name of the chosen NSPrinter.
	Printer string
	// PaperName is the NSPrintInfo paper name, e.g. "iso-a4".
	PaperName string
	// PaperWidth and PaperHeight are the oriented paper size in points.
	PaperWidth, PaperHeight float64
	Orientation             int
	Copies                  int
	// AllPages is true if no page range was chosen; FirstPage and LastPage
	// are the range otherwise.
	AllPages            bool
	FirstPage, LastPage int
	Collate             int
	Duplex              int
	// ColorModel is the "ColorModel" print setting (e.g. "Gray", "RGB"),
	// PrintColorMode the "print-color-mode" one; both may be empty.
	ColorModel     string
	PrintColorMode string
	// Disposition is the job disposition, e.g. DispositionSpool.
	Disposition string
}
