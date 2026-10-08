//go:build windows

package winprint

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/com"
	"github.com/timzifer/goprint/internal/errdefs"
)

var (
	procDocumentPropertiesW = modwinspool.NewProc("DocumentPropertiesW")
	procDeviceCapabilitiesW = modwinspool.NewProc("DeviceCapabilitiesW")

	modprntvpt                        = windows.NewLazySystemDLL("prntvpt.dll")
	procPTOpenProvider                = modprntvpt.NewProc("PTOpenProvider")
	procPTCloseProvider               = modprntvpt.NewProc("PTCloseProvider")
	procPTConvertDevModeToPrintTicket = modprntvpt.NewProc("PTConvertDevModeToPrintTicket")
)

// devMode mirrors the fixed part of DEVMODEW (printer variant). Drivers
// append dmDriverExtra private bytes, so a DEVMODE is handled as []byte and
// this struct overlays its start.
type devMode struct {
	DeviceName    [32]uint16
	SpecVersion   uint16
	DriverVersion uint16
	Size          uint16
	DriverExtra   uint16
	Fields        uint32
	Orientation   int16
	PaperSize     int16
	PaperLength   int16 // 0.1 mm
	PaperWidth    int16 // 0.1 mm
	Scale         int16
	Copies        int16
	DefaultSource int16
	PrintQuality  int16
	Color         int16
	Duplex        int16
	YResolution   int16
	TTOption      int16
	Collate       int16
	FormName      [32]uint16
	LogPixels     uint16
	BitsPerPel    uint32
	PelsWidth     uint32
	PelsHeight    uint32
	Nup           uint32
	DisplayFreq   uint32
	ICMMethod     uint32
	ICMIntent     uint32
	MediaType     uint32
	DitherType    uint32
	Reserved1     uint32
	Reserved2     uint32
	PanningWidth  uint32
	PanningHeight uint32
}

// dmFields bits.
const (
	dmOrientation   = 0x1
	dmPaperSize     = 0x2
	dmPaperLength   = 0x4
	dmPaperWidth    = 0x8
	dmCopies        = 0x100
	dmDefaultSource = 0x200
	dmPrintQuality  = 0x400
	dmColor         = 0x800
	dmDuplex        = 0x1000
	dmCollate       = 0x8000
	dmFormName      = 0x10000
)

const (
	dmOrientPortrait  = 1
	dmOrientLandscape = 2
	dmColorMono       = 1
	dmColorColor      = 2
	dmDupSimplex      = 1
	dmDupVertical     = 2 // long edge
	dmDupHorizontal   = 3 // short edge
	dmResDraft        = -1
	dmResMedium       = -3
	dmResHigh         = -4
	dmPaperUser       = 256
	dmBinManual       = 4
	dmBinAuto         = 7

	dmOutBuffer = 2
	dmInPrompt  = 4
	dmInBuffer  = 8
	idOK        = 1

	dcPapers     = 2
	dcPaperSize  = 3
	dcMaxExtent  = 5
	dcBins       = 6
	dcDuplex     = 7
	dcBinNames   = 12
	dcEnumRes    = 13
	dcPaperNames = 16
	dcCopies     = 18
	dcColor      = 32

	ptJobScope = 2
)

// JobSettings are the settings applied through DEVMODE/PrintTicket.
// Zero values keep the printer default.
type JobSettings struct {
	Copies  int
	Collate *bool
	// PaperWidth and PaperHeight select the paper by size in micrometers.
	PaperWidth, PaperHeight int
	// Orientation: 1 portrait, 2 landscape.
	Orientation int
	// Duplex: 1 simplex, 2 long edge, 3 short edge.
	Duplex int
	// Color: 1 monochrome, 2 color.
	Color int
	// Quality: DMRES_* (-1 draft, -3 medium, -4 high).
	Quality int
	// Tray is a bin name, a bin number, or "auto"/"manual".
	Tray string
	// Scaling places PDF pages on the paper (core.Scale*). Any paper,
	// orientation or scaling setting makes pages use the paper's size.
	Scaling int
}

func (s JobSettings) wantsLayout() bool {
	return s.PaperWidth > 0 || s.Orientation != 0 || s.Scaling != 0
}

// paperDIPs returns the effective paper size of a DEVMODE in DIPs, with
// orientation applied; zero if unknown.
func paperDIPs(printer string, dm []byte) size {
	d := asDevMode(dm)
	var w, h int // micrometers
	if d.PaperSize == dmPaperUser {
		w, h = int(d.PaperWidth)*100, int(d.PaperLength)*100
	}
	if w == 0 || h == 0 {
		caps, err := Capabilities(printer)
		if err != nil {
			return size{}
		}
		for _, p := range caps.Papers {
			if p.ID == d.PaperSize {
				w, h = p.Width, p.Height
			}
		}
	}
	if w == 0 || h == 0 {
		return size{}
	}
	if d.Orientation == dmOrientLandscape {
		w, h = h, w
	}
	const dipPerMicron = 96.0 / 25400
	return size{W: float32(float64(w) * dipPerMicron), H: float32(float64(h) * dipPerMicron)}
}

// Warning is a setting the printer could not apply.
type Warning struct {
	Setting, Message string
}

func (s JobSettings) isZero() bool {
	return s.Copies <= 1 && s.Collate == nil && s.PaperWidth == 0 && s.Orientation == 0 &&
		s.Duplex == 0 && s.Color == 0 && s.Quality == 0 && s.Tray == "" && s.Scaling == 0
}

// Paper is a paper size offered by a printer.
type Paper struct {
	ID            int16
	Name          string // driver's display name
	Width, Height int    // micrometers
}

// Bin is a paper source.
type Bin struct {
	ID   int16
	Name string
}

// Caps are a printer's capabilities as reported by DeviceCapabilitiesW.
type Caps struct {
	Papers      []Paper
	Bins        []Bin
	Duplex      bool
	Color       bool
	Resolutions [][2]int
	MaxCopies   int
	// Custom paper sizes are supported up to this extent (micrometers).
	MaxCustomWidth, MaxCustomHeight int
}

func printerPort(name string) (string, error) {
	ps, err := Printers()
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		if strings.EqualFold(p.Name, name) {
			return p.Port, nil
		}
	}
	return "", fmt.Errorf("%w: %q", errdefs.ErrPrinterNotFound, name)
}

// deviceCaps calls DeviceCapabilitiesW; out may be nil to get the count.
func deviceCaps(device, port *uint16, cap int, out unsafe.Pointer) int {
	r, _, _ := syscall.SyscallN(procDeviceCapabilitiesW.Addr(), uintptr(unsafe.Pointer(device)), uintptr(unsafe.Pointer(port)), uintptr(cap), uintptr(out), 0)
	return int(int32(r))
}

// Capabilities queries the printer's capabilities.
func Capabilities(printer string) (Caps, error) {
	if printer == "" {
		def, err := DefaultPrinter()
		if err != nil {
			return Caps{}, err
		}
		printer = def
	}
	port, err := printerPort(printer)
	if err != nil {
		return Caps{}, err
	}
	dev, _ := windows.UTF16PtrFromString(printer)
	prt, _ := windows.UTF16PtrFromString(port)
	var c Caps

	if n := deviceCaps(dev, prt, dcPapers, nil); n > 0 {
		ids := make([]uint16, n)
		sizes := make([][2]int32, n)
		names := make([][64]uint16, n)
		deviceCaps(dev, prt, dcPapers, unsafe.Pointer(&ids[0]))
		deviceCaps(dev, prt, dcPaperSize, unsafe.Pointer(&sizes[0]))
		deviceCaps(dev, prt, dcPaperNames, unsafe.Pointer(&names[0]))
		for i := range ids {
			c.Papers = append(c.Papers, Paper{
				ID:     int16(ids[i]),
				Name:   windows.UTF16ToString(names[i][:]),
				Width:  int(sizes[i][0]) * 100,
				Height: int(sizes[i][1]) * 100,
			})
		}
	}
	if n := deviceCaps(dev, prt, dcBins, nil); n > 0 {
		ids := make([]uint16, n)
		names := make([][24]uint16, n)
		deviceCaps(dev, prt, dcBins, unsafe.Pointer(&ids[0]))
		deviceCaps(dev, prt, dcBinNames, unsafe.Pointer(&names[0]))
		for i := range ids {
			c.Bins = append(c.Bins, Bin{ID: int16(ids[i]), Name: windows.UTF16ToString(names[i][:])})
		}
	}
	if n := deviceCaps(dev, prt, dcEnumRes, nil); n > 0 {
		res := make([][2]int32, n)
		deviceCaps(dev, prt, dcEnumRes, unsafe.Pointer(&res[0]))
		for _, r := range res {
			c.Resolutions = append(c.Resolutions, [2]int{int(r[0]), int(r[1])})
		}
	}
	c.Duplex = deviceCaps(dev, prt, dcDuplex, nil) == 1
	c.Color = deviceCaps(dev, prt, dcColor, nil) == 1
	c.MaxCopies = deviceCaps(dev, prt, dcCopies, nil)
	if ext := deviceCaps(dev, prt, dcMaxExtent, nil); ext > 0 {
		// The result packs a POINT (0.1 mm) into the return value.
		c.MaxCustomWidth = int(int16(ext&0xFFFF)) * 100
		c.MaxCustomHeight = int(int16(uint32(ext)>>16)) * 100
	}
	return c, nil
}

// documentProperties wraps DocumentPropertiesW without UI.
func documentProperties(h printerHandle, name *uint16, out, in []byte, mode uint32) int32 {
	return documentPropertiesUI(0, h, name, out, in, mode)
}

// documentPropertiesUI wraps DocumentPropertiesW; with dmInPrompt it shows
// the driver's dialog owned by owner.
func documentPropertiesUI(owner windows.HWND, h printerHandle, name *uint16, out, in []byte, mode uint32) int32 {
	var po, pi uintptr
	if len(out) > 0 {
		po = uintptr(unsafe.Pointer(&out[0]))
	}
	if len(in) > 0 {
		pi = uintptr(unsafe.Pointer(&in[0]))
	}
	r, _, _ := syscall.SyscallN(procDocumentPropertiesW.Addr(), uintptr(owner), uintptr(h), uintptr(unsafe.Pointer(name)), po, pi, uintptr(mode))
	return int32(r)
}

// DefaultDevMode returns the printer's default DEVMODE.
func DefaultDevMode(printer string) ([]byte, error) {
	h, err := openPrinter(printer)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	name, _ := windows.UTF16PtrFromString(printer)
	n := documentProperties(h, name, nil, nil, 0)
	if n <= 0 {
		return nil, fmt.Errorf("DocumentPropertiesW(size) failed for %q", printer)
	}
	dm := make([]byte, n)
	if documentProperties(h, name, dm, nil, dmOutBuffer) < 0 {
		return nil, fmt.Errorf("DocumentPropertiesW(get) failed for %q", printer)
	}
	return dm, nil
}

func asDevMode(b []byte) *devMode { return (*devMode)(unsafe.Pointer(&b[0])) }

// BuildDevMode applies s to the printer's default DEVMODE, lets the driver
// validate it and reports settings that did not survive.
func BuildDevMode(printer string, s JobSettings) ([]byte, []Warning, error) {
	return BuildDevModeFrom(printer, nil, s)
}

// BuildDevModeFrom is BuildDevMode starting from base, a DEVMODE for the
// same printer (e.g. from PropertiesDialog), instead of the printer's
// default. A base that does not fit the printer is ignored with a warning.
func BuildDevModeFrom(printer string, base []byte, s JobSettings) ([]byte, []Warning, error) {
	caps, err := Capabilities(printer)
	if err != nil {
		return nil, nil, err
	}
	var warns []Warning
	warn := func(setting, msg string) { warns = append(warns, Warning{setting, msg}) }
	var dm []byte
	if len(base) > 0 {
		if err := checkDevMode(printer, base); err != nil {
			warn(DevModeSetting, err.Error()+"; using the printer defaults")
		} else {
			dm = bytes.Clone(base)
		}
	}
	if dm == nil {
		if dm, err = DefaultDevMode(printer); err != nil {
			return nil, nil, err
		}
	}
	d := asDevMode(dm)

	if s.Copies > 1 {
		// DC_COPIES only covers copies made by the driver; the spooler
		// produces the others, so no limit applies here.
		d.Copies, d.Fields = int16(min(s.Copies, math.MaxInt16)), d.Fields|dmCopies
	}
	if s.Collate != nil {
		d.Collate, d.Fields = 0, d.Fields|dmCollate
		if *s.Collate {
			d.Collate = 1
		}
	}
	if s.PaperWidth > 0 && s.PaperHeight > 0 {
		if p, ok := matchPaper(caps.Papers, s.PaperWidth, s.PaperHeight); ok {
			d.PaperSize = p.ID
			d.Fields = (d.Fields | dmPaperSize) &^ (dmPaperLength | dmPaperWidth | dmFormName)
		} else if caps.MaxCustomWidth >= s.PaperWidth && caps.MaxCustomHeight >= s.PaperHeight {
			d.PaperSize = dmPaperUser
			d.PaperWidth = int16(s.PaperWidth / 100)
			d.PaperLength = int16(s.PaperHeight / 100)
			d.Fields = (d.Fields | dmPaperSize | dmPaperWidth | dmPaperLength) &^ dmFormName
		} else {
			warn("Media", "paper size not offered by the printer")
		}
	}
	if s.Orientation != 0 {
		d.Orientation, d.Fields = int16(s.Orientation), d.Fields|dmOrientation
	}
	if s.Duplex != 0 {
		if s.Duplex != dmDupSimplex && !caps.Duplex {
			warn("Duplex", "printer cannot print two-sided")
		} else {
			d.Duplex, d.Fields = int16(s.Duplex), d.Fields|dmDuplex
		}
	}
	if s.Color != 0 {
		if s.Color == dmColorColor && !caps.Color {
			warn("Color", "printer cannot print in color")
		} else {
			d.Color, d.Fields = int16(s.Color), d.Fields|dmColor
		}
	}
	if s.Quality != 0 {
		d.PrintQuality, d.Fields = int16(s.Quality), d.Fields|dmPrintQuality
	}
	if s.Tray != "" {
		if id, ok := matchBin(caps.Bins, s.Tray); ok {
			d.DefaultSource, d.Fields = id, d.Fields|dmDefaultSource
		} else {
			warn("Tray", fmt.Sprintf("printer has no paper source %q", s.Tray))
		}
	}

	// Let the driver validate and merge.
	want := *d
	h, err := openPrinter(printer)
	if err != nil {
		return nil, nil, err
	}
	defer h.Close()
	name, _ := windows.UTF16PtrFromString(printer)
	n := documentProperties(h, name, nil, nil, 0)
	if n <= 0 {
		return nil, nil, fmt.Errorf("DocumentPropertiesW(size) failed for %q", printer)
	}
	merged := make([]byte, n)
	if documentProperties(h, name, merged, dm, dmInBuffer|dmOutBuffer) < 0 {
		return nil, nil, fmt.Errorf("DocumentPropertiesW(merge) failed for %q", printer)
	}
	got := asDevMode(merged)
	check := func(setting string, field uint32, w, g int16) {
		if want.Fields&field != 0 && w != g {
			warn(setting, fmt.Sprintf("driver changed the value (%d → %d)", w, g))
		}
	}
	check("Copies", dmCopies, want.Copies, got.Copies)
	check("Orientation", dmOrientation, want.Orientation, got.Orientation)
	check("Duplex", dmDuplex, want.Duplex, got.Duplex)
	check("Color", dmColor, want.Color, got.Color)
	check("Collate", dmCollate, want.Collate, got.Collate)
	check("Media", dmPaperSize, want.PaperSize, got.PaperSize)
	return merged, warns, nil
}

// matchPaper finds the printer paper with the given size (±1 mm).
func matchPaper(papers []Paper, w, h int) (Paper, bool) {
	const tol = 1000
	for _, p := range papers {
		if abs(p.Width-w) <= tol && abs(p.Height-h) <= tol {
			return p, true
		}
	}
	return Paper{}, false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// matchBin resolves a tray name.
func matchBin(bins []Bin, tray string) (int16, bool) {
	switch strings.ToLower(tray) {
	case "auto":
		return dmBinAuto, true
	case "manual":
		return dmBinManual, true
	}
	for _, b := range bins {
		if strings.EqualFold(strings.TrimSpace(b.Name), strings.TrimSpace(tray)) {
			return b.ID, true
		}
	}
	if n, err := strconv.Atoi(tray); err == nil {
		for _, b := range bins {
			if int(b.ID) == n {
				return b.ID, true
			}
		}
	}
	return 0, false
}

// PrintTicket converts a DEVMODE into a job-scope PrintTicket (XML).
func PrintTicket(printer string, dm []byte) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(printer)
	if err != nil {
		return nil, err
	}
	var provider uintptr
	if err := com.Call(procPTOpenProvider, uintptr(unsafe.Pointer(name)), 1, uintptr(unsafe.Pointer(&provider))); err != nil {
		return nil, err
	}
	defer syscall.SyscallN(procPTCloseProvider.Addr(), provider)
	stream, err := com.NewHGlobalStream()
	if err != nil {
		return nil, err
	}
	defer stream.Release()
	if err := com.Call(procPTConvertDevModeToPrintTicket, provider, uintptr(len(dm)), uintptr(unsafe.Pointer(&dm[0])), ptJobScope, stream.Ptr()); err != nil {
		return nil, err
	}
	if err := stream.Rewind(); err != nil {
		return nil, err
	}
	return io.ReadAll(stream)
}
