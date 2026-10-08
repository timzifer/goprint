//go:build windows

package winprint

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/timzifer/goprint/internal/com"
	"github.com/timzifer/goprint/internal/core"
	"github.com/timzifer/goprint/internal/errdefs"
)

// The classic print dialog (PrintDlgExW) can preselect the printer and
// every DEVMODE setting, and it does not create a job by itself, so it
// also serves "settings only" requests. It has no preview.

var (
	modcomdlg32      = windows.NewLazySystemDLL("comdlg32.dll")
	procPrintDlgExW  = modcomdlg32.NewProc("PrintDlgExW")
	modkernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalAlloc  = modkernel32.NewProc("GlobalAlloc")
	procGlobalLock   = modkernel32.NewProc("GlobalLock")
	procGlobalUnlock = modkernel32.NewProc("GlobalUnlock")
	procGlobalFree   = modkernel32.NewProc("GlobalFree")
	procGlobalSize   = modkernel32.NewProc("GlobalSize")
)

const (
	pdPageNums                   = 0x2
	pdNoSelection                = 0x4
	pdCollate                    = 0x10
	pdUseDevModeCopiesAndCollate = 0x40000
	pdNoCurrentPage              = 0x800000
	startPageGeneral             = 0xFFFFFFFF
	pdResultCancel               = 0
	pdResultPrint                = 1
	pdResultApply                = 2
	gmemMoveable                 = 0x2
	maxPageRanges                = 32
)

// printDlgEx mirrors PRINTDLGEXW (natural alignment on 64-bit; on 32-bit
// the header packs to 1 byte, which gives the same layout since every field
// is 4 bytes wide there).
type printDlgEx struct {
	StructSize       uint32
	Owner            uintptr
	DevMode          uintptr
	DevNames         uintptr
	DC               uintptr
	Flags            uint32
	Flags2           uint32
	ExclusionFlags   uint32
	NumPageRanges    uint32
	MaxPageRanges    uint32
	PageRanges       *printPageRange
	MinPage          uint32
	MaxPage          uint32
	Copies           uint32
	Instance         uintptr
	PrintTemplate    *uint16
	Callback         uintptr
	NumPropertyPages uint32
	PropertyPages    uintptr
	StartPage        uint32
	ResultAction     uint32
}

type printPageRange struct{ From, To uint32 }

// ClassicOptions configures ClassicDialog.
type ClassicOptions struct {
	Owner   windows.HWND
	Title   string
	Printer string // preselected printer; empty = default
	// Settings preset the dialog through the printer's DEVMODE, or through
	// BaseDevMode if set (e.g. from PropertiesDialog).
	Settings    JobSettings
	BaseDevMode []byte
	PageRanges  []core.PageRange
	PrintNow    bool
}

// ClassicResult is the outcome of a confirmed classic dialog.
type ClassicResult struct {
	Printer    string
	Chosen     JobSettings // read back from the DEVMODE
	PageRanges []core.PageRange
	Warnings   []Warning // presets the printer could not take
	Job        *Job      // nil unless PrintNow
	// DevMode is the confirmed DEVMODE, driver-private parts included.
	DevMode []byte
}

// ClassicDialog shows PrintDlgExW for the PDF from src.
func ClassicDialog(ctx context.Context, src io.Reader, opts ClassicOptions) (*ClassicResult, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("read PDF: %w", err)
	}
	if opts.Printer == "" {
		if opts.Printer, err = DefaultPrinter(); err != nil {
			return nil, err
		}
	}
	a, err := com.NewApartment(com.OleSTA)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	var res *ClassicResult
	err = a.Do(ctx, func() error {
		var err error
		res, err = runClassic(ctx, data, opts)
		return err
	})
	if err != nil || !opts.PrintNow {
		return res, err
	}
	// Print with exactly what the user confirmed.
	if len(res.DevMode) == 0 {
		return nil, fmt.Errorf("winprint: dialog returned no DEVMODE")
	}
	job, err := Print(ctx, bytes.NewReader(data), Options{
		Printer:    res.Printer,
		Title:      opts.Title,
		DevMode:    res.DevMode,
		PageRanges: res.PageRanges,
	})
	if err != nil {
		return nil, err
	}
	res.Job = job
	return res, nil
}

func runClassic(ctx context.Context, data []byte, opts ClassicOptions) (*ClassicResult, error) {
	doc, err := loadPDFBytes(ctx, data)
	if err != nil {
		return nil, err
	}
	pages := doc.pages
	doc.Close()

	dm, warns, err := BuildDevModeFrom(opts.Printer, opts.BaseDevMode, opts.Settings)
	if err != nil {
		return nil, err
	}
	hDevMode, err := globalFrom(dm)
	if err != nil {
		return nil, err
	}
	defer func() { globalFree(hDevMode) }()
	port, err := printerPort(opts.Printer)
	if err != nil {
		return nil, err
	}
	hDevNames, err := globalFrom(devNames(opts.Printer, port))
	if err != nil {
		return nil, err
	}
	defer func() { globalFree(hDevNames) }()

	owner := opts.Owner
	if owner == 0 {
		if owner, err = com.HelperWindow(opts.Title); err != nil {
			return nil, err
		}
		defer com.DestroyWindow(owner)
	}

	ranges := make([]printPageRange, maxPageRanges)
	pd := printDlgEx{
		Owner:         uintptr(owner),
		DevMode:       hDevMode,
		DevNames:      hDevNames,
		Flags:         pdUseDevModeCopiesAndCollate | pdNoSelection | pdNoCurrentPage,
		MaxPageRanges: maxPageRanges,
		PageRanges:    &ranges[0],
		MinPage:       1,
		MaxPage:       uint32(max(pages, 1)),
		Copies:        1,
		StartPage:     startPageGeneral,
	}
	pd.StructSize = uint32(unsafe.Sizeof(pd))
	for _, r := range opts.PageRanges {
		if int(pd.NumPageRanges) == maxPageRanges {
			break
		}
		to := r.To
		if to == 0 || to > pages {
			to = pages
		}
		ranges[pd.NumPageRanges] = printPageRange{uint32(r.From), uint32(to)}
		pd.NumPageRanges++
		pd.Flags |= pdPageNums
	}

	tracef("calling PrintDlgExW (%d pages, printer %q)", pages, opts.Printer)
	if err := com.Call(procPrintDlgExW, uintptr(unsafe.Pointer(&pd))); err != nil {
		return nil, fmt.Errorf("PrintDlgExW: %w", err)
	}
	// The dialog may have replaced the handles.
	hDevMode, hDevNames = pd.DevMode, pd.DevNames
	tracef("classic dialog result %d, flags 0x%X", pd.ResultAction, pd.Flags)
	if pd.ResultAction != pdResultPrint {
		return nil, errdefs.ErrCanceled
	}

	res := &ClassicResult{Warnings: warns}
	if b, err := globalBytes(hDevNames); err == nil {
		res.Printer = deviceFromDevNames(b)
	}
	if res.Printer == "" {
		res.Printer = opts.Printer
	}
	if b, err := globalBytes(hDevMode); err == nil && len(b) >= int(unsafe.Sizeof(devMode{})) {
		res.DevMode = b
		res.Chosen = readDevMode(res.Printer, b)
	}
	if pd.Flags&pdPageNums != 0 {
		for _, r := range ranges[:pd.NumPageRanges] {
			res.PageRanges = append(res.PageRanges, core.PageRange{From: int(r.From), To: int(r.To)})
		}
	}
	return res, nil
}

// readDevMode turns a DEVMODE back into JobSettings.
func readDevMode(printer string, dm []byte) JobSettings {
	d := asDevMode(dm)
	var s JobSettings
	if d.Fields&dmCopies != 0 {
		s.Copies = int(d.Copies)
	}
	if d.Fields&dmCollate != 0 {
		c := d.Collate == 1
		s.Collate = &c
	}
	if d.Fields&dmOrientation != 0 {
		s.Orientation = int(d.Orientation)
	}
	if d.Fields&dmDuplex != 0 {
		s.Duplex = int(d.Duplex)
	}
	if d.Fields&dmColor != 0 {
		s.Color = int(d.Color)
	}
	if d.Fields&dmPrintQuality != 0 && d.PrintQuality < 0 {
		s.Quality = int(d.PrintQuality)
	}
	if p := paperDIPs(printer, dm); p.W > 0 {
		// Report the paper itself, not its orientation.
		w, h := int(float64(p.W)*25400/96+0.5), int(float64(p.H)*25400/96+0.5)
		if s.Orientation == dmOrientLandscape {
			w, h = h, w
		}
		s.PaperWidth, s.PaperHeight = w, h
	}
	if d.Fields&dmDefaultSource != 0 {
		if caps, err := Capabilities(printer); err == nil {
			for _, b := range caps.Bins {
				if b.ID == d.DefaultSource {
					s.Tray = b.Name
				}
			}
		}
	}
	return s
}

// devNames builds a DEVNAMES block: four WORD offsets (in WCHARs) followed
// by driver, device and port strings.
func devNames(device, port string) []byte {
	strs := []string{"winspool", device, port}
	words := []uint16{0, 0, 0, 0}
	off := uint16(4)
	var tail []uint16
	for i, s := range strs {
		u, _ := windows.UTF16FromString(s)
		words[i] = off + uint16(len(tail))
		tail = append(tail, u...)
	}
	all := append(words, tail...)
	return unsafe.Slice((*byte)(unsafe.Pointer(&all[0])), len(all)*2)
}

func deviceFromDevNames(b []byte) string {
	if len(b) < 8 {
		return ""
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2)
	off := int(u[1])
	if off >= len(u) {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(u[off:]))
}

// globalFrom copies b into a movable global memory block.
func globalFrom(b []byte) (uintptr, error) {
	h, _, e := procGlobalAlloc.Call(gmemMoveable, uintptr(len(b)))
	if h == 0 {
		return 0, fmt.Errorf("GlobalAlloc: %w", e)
	}
	p, _, e := procGlobalLock.Call(h)
	if p == 0 {
		globalFree(h)
		return 0, fmt.Errorf("GlobalLock: %w", e)
	}
	dst := unsafe.Slice((*byte)(com.Ptr(p)), len(b))
	//lint:ignore S1001 copy() into this slice trips the Go 1.26.0 compiler.
	for i := range b {
		dst[i] = b[i]
	}
	procGlobalUnlock.Call(h)
	return h, nil
}

// globalBytes copies a global memory block.
func globalBytes(h uintptr) ([]byte, error) {
	if h == 0 {
		return nil, fmt.Errorf("winprint: nil global handle")
	}
	n, _, _ := procGlobalSize.Call(h)
	p, _, e := procGlobalLock.Call(h)
	if p == 0 {
		return nil, fmt.Errorf("GlobalLock: %w", e)
	}
	defer procGlobalUnlock.Call(h)
	src := unsafe.Slice((*byte)(com.Ptr(p)), n)
	out := make([]byte, n)
	copy(out, src)
	return out, nil
}

func globalFree(h uintptr) {
	if h != 0 {
		procGlobalFree.Call(h)
	}
}

// LegacyDialogRedirected reports whether PrintDlgEx is shown as the modern
// "print from a Win32 app" dialog, which ignores the printer preselection
// in DEVNAMES (observed on Windows 11 24H2; DEVMODE settings still apply).
// No workaround was found: the redirected dialog always starts on the
// default printer, whether the printer is named in DEVNAMES, in
// DEVMODE.dmDeviceName, in both or in neither, and it does not recall the
// printer the user confirmed in an earlier run of the same executable.
// Windows 11 redirects unless the user set PreferLegacyPrintDialog. To keep
// RequirePrinter's promise, every Windows 11 build counts as redirected
// unless that setting is present.
func LegacyDialogRedirected() bool {
	if windows.RtlGetVersion().BuildNumber < 22000 {
		return false
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Print\UnifiedPrintDialog`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("PreferLegacyPrintDialog")
	return err != nil || v == 0
}
