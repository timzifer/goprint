//go:build darwin

package macprint

import (
	"fmt"

	"github.com/ebitengine/purego/objc"

	"github.com/timzifer/goprint/internal/errdefs"
)

// NSPrintPanelOptions.
const (
	panelShowsCopies             = 1 << 0
	panelShowsPageRange          = 1 << 1
	panelShowsPaperSize          = 1 << 2
	panelShowsOrientation        = 1 << 3
	panelShowsScaling            = 1 << 4
	panelShowsPageSetupAccessory = 1 << 8
	panelShowsPreview            = 1 << 17
	panelOptions                 = panelShowsCopies | panelShowsPageRange | panelShowsPaperSize | panelShowsOrientation | panelShowsScaling | panelShowsPageSetupAccessory
	nsModalResponseOK            = 1
	activationPolicyAccessory    = 1
	activationPolicyRegular      = 0
)

// session holds the PDFKit document and the preset NSPrintInfo shared by
// the dialog and PrintToFile, so both build the print operation the same
// way.
type session struct {
	doc, info objc.ID
	pages     int
}

func (s *session) release() {
	if s.info != 0 {
		s.info.Send(selRelease)
	}
	if s.doc != 0 {
		s.doc.Send(selRelease)
	}
}

// newSession loads pdf into a PDFDocument and creates an NSPrintInfo preset
// with o. The caller must release it.
func newSession(pdf []byte, o Options) (*session, error) {
	if len(pdf) == 0 {
		return nil, fmt.Errorf("%w: empty PDF", errdefs.ErrInvalid)
	}
	data := objc.ID(clsNSData).Send(selDataWithBytesLength, &pdf[0], len(pdf))
	if data == 0 {
		return nil, fmt.Errorf("macprint: NSData dataWithBytes:length: returned nil")
	}
	s := &session{}
	if s.doc = objc.ID(clsPDFDocument).Send(selAlloc).Send(selInitWithData, data); s.doc == 0 {
		return nil, fmt.Errorf("%w: PDFKit cannot read the document", errdefs.ErrInvalid)
	}
	if objc.Send[bool](s.doc, selIsLocked) {
		s.release()
		return nil, fmt.Errorf("%w: the PDF is encrypted", errdefs.ErrUnsupported)
	}
	if s.pages = objc.Send[int](s.doc, selPageCount); s.pages < 1 {
		s.release()
		return nil, fmt.Errorf("%w: the PDF has no pages", errdefs.ErrInvalid)
	}
	if s.info = objc.ID(clsNSPrintInfo).Send(selAlloc).Send(selInit); s.info == 0 {
		s.release()
		return nil, fmt.Errorf("macprint: NSPrintInfo init returned nil")
	}
	if err := s.apply(o); err != nil {
		s.release()
		return nil, err
	}
	return s, nil
}

// apply presets the NSPrintInfo. PrintCore settings go first: the
// updateFromPMPrintSettings that publishes them rewrites the AppKit
// dictionary, so dictionary values (copies, page range) are set after it.
func (s *session) apply(o Options) error {
	if o.Printer != "" {
		p, err := findPrinter(o.Printer)
		if err != nil {
			return err
		}
		s.info.Send(selSetPrinter, p)
	}

	ps := objc.Send[uintptr](s.info, selPMPrintSettings)
	if ps == 0 {
		return fmt.Errorf("macprint: NSPrintInfo has no PMPrintSettings")
	}
	if o.Copies > 0 {
		if err := osErr("PMSetCopies", pmSetCopies(ps, uint32(o.Copies), false)); err != nil {
			return err
		}
	}
	if o.Collate != Default {
		if err := osErr("PMSetCollate", pmSetCollate(ps, o.Collate == Yes)); err != nil {
			return err
		}
	}
	if o.Duplex != 0 {
		if err := osErr("PMSetDuplex", pmSetDuplex(ps, uint32(o.Duplex))); err != nil {
			return err
		}
	}
	set := func(k, v string) error {
		return osErr("PMPrintSettingsSetValue("+k+")", pmPrintSettingsSetValue(ps, nsString(k), nsString(v), false))
	}
	// "ColorModel" is the PPD option of most drivers (and of the IPP
	// Everywhere/AirPrint PPDs CUPS generates); "print-color-mode" reaches
	// CUPS as the IPP attribute for driverless queues.
	switch o.Color {
	case Yes:
		if err := set("ColorModel", "RGB"); err != nil {
			return err
		}
		if err := set("print-color-mode", "color"); err != nil {
			return err
		}
	case No:
		if err := set("ColorModel", "Gray"); err != nil {
			return err
		}
		if err := set("print-color-mode", "monochrome"); err != nil {
			return err
		}
	}
	for k, v := range o.Extra {
		if err := set(k, v); err != nil {
			return err
		}
	}
	s.info.Send(selUpdateFromPMSettings)

	dict := s.info.Send(selDictionary)
	if dict == 0 {
		return fmt.Errorf("macprint: NSPrintInfo has no dictionary")
	}
	if o.Copies > 0 {
		dict.Send(selSetObjectForKey, nsInteger(o.Copies), kPrintCopies)
	}
	if o.FirstPage > 0 {
		first, last := o.FirstPage, o.LastPage
		if last == 0 || last > s.pages {
			last = s.pages
		}
		if first > s.pages {
			return fmt.Errorf("%w: page %d is beyond the document's %d pages", errdefs.ErrInvalid, first, s.pages)
		}
		dict.Send(selSetObjectForKey, nsBool(false), kPrintAllPages)
		dict.Send(selSetObjectForKey, nsInteger(first), kPrintFirstPage)
		dict.Send(selSetObjectForKey, nsInteger(last), kPrintLastPage)
	} else {
		dict.Send(selSetObjectForKey, nsBool(true), kPrintAllPages)
	}

	// NSPrintInfo keeps paperSize oriented: set the size as it lies for the
	// requested orientation, then the orientation.
	if o.PaperWidth > 0 && o.PaperHeight > 0 {
		w, h := min(o.PaperWidth, o.PaperHeight), max(o.PaperWidth, o.PaperHeight)
		if o.SetOrientation && o.Orientation == OrientationLandscape {
			w, h = h, w
		}
		msgSendSize(s.info, selSetPaperSize, nsSize{W: w, H: h})
	}
	if o.SetOrientation {
		s.info.Send(selSetOrientation, o.Orientation)
	}
	return nil
}

// findPrinter resolves a CUPS queue name or an NSPrinter (display) name.
func findPrinter(name string) (objc.ID, error) {
	if p := objc.ID(clsNSPrinter).Send(selPrinterWithName, nsString(name)); p != 0 {
		return p, nil
	}
	// NSPrinter names are the user-visible names; PrintCore maps a queue
	// name (printer ID) to that name.
	if pm := pmPrinterCreateFromPrinterID(nsString(name)); pm != 0 {
		display := goString(pmPrinterGetName(pm))
		pmRelease(pm)
		if display != "" {
			if p := objc.ID(clsNSPrinter).Send(selPrinterWithName, nsString(display)); p != 0 {
				return p, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: %q", errdefs.ErrPrinterNotFound, name)
}

// operation creates the PDFKit print operation for the session.
func (s *session) operation(o Options) (objc.ID, error) {
	op := s.doc.Send(selPrintOperation, s.info, o.Scaling, o.AutoRotate)
	if op == 0 {
		return 0, fmt.Errorf("macprint: PDFDocument printOperationForPrintInfo: returned nil")
	}
	if o.Title != "" {
		op.Send(selSetJobTitle, nsString(o.Title))
	}
	return op, nil
}

// result reads the settings back from an NSPrintInfo.
func result(info objc.ID) Result {
	var r Result
	if p := info.Send(selPrinter); p != 0 {
		r.Printer = goString(p.Send(selName))
	}
	if sess := objc.Send[uintptr](info, selPMPrintSession); sess != 0 {
		var pm uintptr
		if pmSessionGetCurrentPrinter(sess, &pm) == 0 && pm != 0 {
			if id := goString(pmPrinterGetID(pm)); id != "" {
				r.Printer = id
			}
		}
	}
	r.PaperName = goString(info.Send(selPaperName))
	sz := msgSendGetSize(info, selPaperSize)
	r.PaperWidth, r.PaperHeight = sz.W, sz.H
	r.Orientation = objc.Send[int](info, selOrientation)
	r.Disposition = goString(info.Send(selJobDisposition))

	r.AllPages = true
	if dict := info.Send(selDictionary); dict != 0 {
		num := func(key objc.ID) objc.ID {
			v := dict.Send(selObjectForKey, key)
			if v == 0 || !objc.Send[bool](v, selIsKindOfClass, clsNSNumber) {
				return 0
			}
			return v
		}
		if v := num(kPrintCopies); v != 0 {
			r.Copies = objc.Send[int](v, selIntegerValue)
		}
		if v := num(kPrintAllPages); v != 0 {
			r.AllPages = objc.Send[bool](v, selBoolValue)
		}
		if v := num(kPrintFirstPage); v != 0 {
			r.FirstPage = objc.Send[int](v, selIntegerValue)
		}
		if v := num(kPrintLastPage); v != 0 {
			r.LastPage = objc.Send[int](v, selIntegerValue)
		}
	}

	if ps := objc.Send[uintptr](info, selPMPrintSettings); ps != 0 {
		var collate bool
		if pmGetCollate(ps, &collate) == 0 {
			r.Collate = No
			if collate {
				r.Collate = Yes
			}
		}
		var duplex uint32
		if pmGetDuplex(ps, &duplex) == 0 {
			r.Duplex = int(duplex)
		}
		get := func(k string) string {
			var v objc.ID
			if pmPrintSettingsGetValue(ps, nsString(k), &v) != 0 {
				return ""
			}
			return goString(v)
		}
		r.ColorModel = get("ColorModel")
		r.PrintColorMode = get("print-color-mode")
	}
	return r
}

// PrintToFile runs the same PDFKit print operation as Dialog, without any
// panel, and saves the output as PDF to path (NSPrintSaveJob). It exists
// so that presets and PDFKit printing can be tested non-interactively. It
// runs on the main thread (see OnMain).
func PrintToFile(pdf []byte, o Options, path string) (Result, error) {
	var r Result
	var err error
	if lerr := OnMain(func() { r, err = printToFile(pdf, o, path) }); lerr != nil {
		return Result{}, lerr
	}
	return r, err
}

func printToFile(pdf []byte, o Options, path string) (Result, error) {
	if err := load(); err != nil {
		return Result{}, err
	}
	defer autoreleasePool()()
	s, err := newSession(pdf, o)
	if err != nil {
		return Result{}, err
	}
	defer s.release()
	url := objc.ID(clsNSURL).Send(selFileURLWithPath, nsString(path))
	if url == 0 {
		return Result{}, fmt.Errorf("%w: invalid output path %q", errdefs.ErrInvalid, path)
	}
	dict := s.info.Send(selDictionary)
	dict.Send(selSetObjectForKey, kPrintSaveJob, kPrintJobDisposition)
	dict.Send(selSetObjectForKey, url, kPrintJobSavingURL)

	op, err := s.operation(o)
	if err != nil {
		return Result{}, err
	}
	op.Send(selSetShowsPrintPanel, false)
	op.Send(selSetShowsProgressPanel, false)
	if !objc.Send[bool](op, selRunOperation) {
		return Result{}, fmt.Errorf("macprint: print operation failed")
	}
	info := op.Send(selPrintInfo)
	if info == 0 {
		info = s.info
	}
	return result(info), nil
}

// Dialog shows the print panel preset with o and returns the user's
// choice. With printNow it runs the PDFKit print operation with the panel
// and its live preview, and the operation prints (or saves, or opens
// Preview, as the user picks in the panel). Without printNow it shows the
// bare NSPrintPanel for the NSPrintInfo, which never creates a job but has
// no preview. It returns ErrCanceled if the user cancels and runs on the
// main thread (see OnMain).
//
// The panel is app-modal; a sheet on an owner window is not implemented.
func Dialog(pdf []byte, o Options, printNow bool) (Result, error) {
	var r Result
	var err error
	if lerr := OnMain(func() { r, err = dialog(pdf, o, printNow) }); lerr != nil {
		return Result{}, lerr
	}
	return r, err
}

func dialog(pdf []byte, o Options, printNow bool) (Result, error) {
	if err := load(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", errdefs.ErrNoDialog, err)
	}
	defer autoreleasePool()()
	s, err := newSession(pdf, o)
	if err != nil {
		return Result{}, err
	}
	defer s.release()
	restore, err := ensureApp()
	if err != nil {
		return Result{}, err
	}
	defer restore()

	if !printNow || o.Accept != nil {
		panel := objc.ID(clsNSPrintPanel).Send(selPrintPanel)
		if panel == 0 {
			return Result{}, fmt.Errorf("%w: NSPrintPanel printPanel returned nil", errdefs.ErrNoDialog)
		}
		panel.Send(selSetOptions, panelOptions)
		if objc.Send[int](panel, selRunModalWithPrintInfo, s.info) != nsModalResponseOK {
			return Result{}, errdefs.ErrCanceled
		}
		r := result(s.info)
		if o.Accept != nil {
			if err := o.Accept(r); err != nil {
				return Result{}, err
			}
		}
		if !printNow {
			return r, nil
		}
		// Print what the panel confirmed, without showing it again.
		op, err := s.operation(o)
		if err != nil {
			return Result{}, err
		}
		op.Send(selSetShowsPrintPanel, false)
		op.Send(selSetShowsProgressPanel, true)
		if !objc.Send[bool](op, selRunOperation) {
			return Result{}, fmt.Errorf("macprint: print operation failed")
		}
		return r, nil
	}

	op, err := s.operation(o)
	if err != nil {
		return Result{}, err
	}
	op.Send(selSetShowsPrintPanel, true)
	op.Send(selSetShowsProgressPanel, true)
	panel := op.Send(selPrintPanel)
	if panel == 0 {
		return Result{}, fmt.Errorf("%w: print operation has no print panel", errdefs.ErrNoDialog)
	}
	panel.Send(selSetOptions, panelOptions|panelShowsPreview)
	// runOperation returns NO both when the user cancels and when the
	// operation fails; AppKit does not tell them apart.
	if !objc.Send[bool](op, selRunOperation) {
		return Result{}, errdefs.ErrCanceled
	}
	info := op.Send(selPrintInfo)
	if info == 0 {
		info = s.info
	}
	return result(info), nil
}

// ensureApp makes sure an NSApplication exists and is in front. Programs
// without their own Cocoa app get a minimal accessory app (no Dock icon,
// no menu bar) for the duration of the dialog; restore undoes the policy
// change.
func ensureApp() (restore func(), err error) {
	app := objc.ID(clsNSApplication).Send(selSharedApplication)
	if app == 0 {
		return nil, fmt.Errorf("%w: NSApplication sharedApplication returned nil", errdefs.ErrNoDialog)
	}
	restore = func() {}
	if !objc.Send[bool](app, selIsRunning) {
		prev := objc.Send[int](app, selActivationPolicy)
		if prev != activationPolicyRegular && prev != activationPolicyAccessory {
			objc.Send[bool](app, selSetActivationPolicy, activationPolicyAccessory)
			restore = func() { objc.Send[bool](app, selSetActivationPolicy, prev) }
		}
	}
	app.Send(selActivateIgnoringOthers, true)
	return restore, nil
}
