//go:build windows

package winprint

import (
	"context"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/com"
	"github.com/timzifer/goprint/internal/errdefs"
)

// DevModeSetting names a DEVMODE passed in by the caller in warnings.
const DevModeSetting = "Vendor[windows:devmode]"

// PropertiesResult is the outcome of a confirmed driver dialog.
type PropertiesResult struct {
	Printer string
	// DevMode holds everything the user chose, driver-private parts
	// included; pass it back as Options.BaseDevMode.
	DevMode []byte
	// Chosen are the standard settings read back from DevMode.
	Chosen   JobSettings
	Warnings []Warning // presets the printer could not take
}

// PropertiesDialog shows the printer driver's own settings dialog
// ("Printing preferences"), preset with base (a DEVMODE for that printer,
// may be nil) and s on top. It blocks until the user closes it; ctx is
// only checked before the dialog opens. Cancel returns ErrCanceled.
func PropertiesDialog(ctx context.Context, owner windows.HWND, printer string, base []byte, s JobSettings) (*PropertiesResult, error) {
	if printer == "" {
		def, err := DefaultPrinter()
		if err != nil {
			return nil, err
		}
		printer = def
	}
	dm, warns, err := BuildDevModeFrom(printer, base, s)
	if err != nil {
		return nil, err
	}
	a, err := com.NewApartment(com.OleSTA)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	var out []byte
	err = a.Do(ctx, func() error {
		var err error
		out, err = runProperties(owner, printer, dm)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &PropertiesResult{Printer: printer, DevMode: out, Chosen: readDevMode(printer, out), Warnings: warns}, nil
}

func runProperties(owner windows.HWND, printer string, dm []byte) ([]byte, error) {
	h, err := openPrinter(printer)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	if owner == 0 {
		if owner, err = com.HelperWindow(printer); err != nil {
			return nil, err
		}
		defer com.DestroyWindow(owner)
	}
	name, _ := windows.UTF16PtrFromString(printer)
	n := documentProperties(h, name, nil, nil, 0)
	if n <= 0 {
		return nil, fmt.Errorf("DocumentPropertiesW(size) failed for %q", printer)
	}
	out := make([]byte, n)
	tracef("calling DocumentPropertiesW with prompt (printer %q)", printer)
	r := documentPropertiesUI(owner, h, name, out, dm, dmInBuffer|dmOutBuffer|dmInPrompt)
	switch {
	case r < 0:
		return nil, fmt.Errorf("DocumentPropertiesW(prompt) failed for %q", printer)
	case r != idOK:
		return nil, errdefs.ErrCanceled
	}
	return out, nil
}

// checkDevMode reports whether dm is a complete DEVMODE for printer.
func checkDevMode(printer string, dm []byte) error {
	if len(dm) < int(unsafe.Sizeof(devMode{})) {
		return fmt.Errorf("DEVMODE too short (%d bytes)", len(dm))
	}
	d := asDevMode(dm)
	if int(d.Size)+int(d.DriverExtra) > len(dm) || int(d.Size) < int(unsafe.Offsetof(d.FormName)) {
		return fmt.Errorf("DEVMODE size %d+%d does not match %d bytes", d.Size, d.DriverExtra, len(dm))
	}
	// dmDeviceName holds at most 31 characters of the printer name.
	got := windows.UTF16ToString(d.DeviceName[:])
	want := windows.StringToUTF16(printer)
	if len(want) > len(d.DeviceName) {
		want = append(want[:len(d.DeviceName)-1], 0)
	}
	if !strings.EqualFold(got, windows.UTF16ToString(want)) {
		return fmt.Errorf("DEVMODE is for printer %q, not %q", got, printer)
	}
	return nil
}
