//go:build windows

package winprint

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/com"
)

var (
	procPTGetPrintCapabilities = modprntvpt.NewProc("PTGetPrintCapabilities")
	modoleaut32                = windows.NewLazySystemDLL("oleaut32.dll")
	procSysFreeString          = modoleaut32.NewProc("SysFreeString")
)

// Qualities returns the print qualities the printer offers, as DMRES_*
// values (draft, medium, high), read from its PrintCapabilities
// (psk:PageOutputQuality). Empty if the printer does not say.
func Qualities(ctx context.Context, printer string) ([]int, error) {
	a, err := apartment()
	if err != nil {
		return nil, err
	}
	var caps []byte
	err = a.Do(ctx, func() error {
		var err error
		caps, err = printCapabilities(printer)
		return err
	})
	if err != nil {
		return nil, err
	}
	return parseQualities(caps), nil
}

func printCapabilities(printer string) ([]byte, error) {
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
	var msg uintptr // BSTR
	err = com.Call(procPTGetPrintCapabilities, provider, 0, stream.Ptr(), uintptr(unsafe.Pointer(&msg)))
	if msg != 0 {
		syscall.SyscallN(procSysFreeString.Addr(), msg)
	}
	if err != nil {
		return nil, err
	}
	if err := stream.Rewind(); err != nil {
		return nil, err
	}
	return io.ReadAll(stream)
}

// parseQualities reads the options of psk:PageOutputQuality. Names are
// matched by their local part; other (vendor) options are skipped.
func parseQualities(caps []byte) []int {
	byName := map[string]int{"Draft": dmResDraft, "Normal": dmResMedium, "High": dmResHigh, "Photographic": dmResHigh}
	var out []int
	add := func(q int) {
		for _, v := range out {
			if v == q {
				return
			}
		}
		out = append(out, q)
	}
	d := xml.NewDecoder(bytes.NewReader(caps))
	depth, in := 0, -1 // in: depth of the quality feature, -1 outside
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			name := localName(attr(t, "name"))
			switch {
			case t.Name.Local == "Feature" && in < 0 && name == "PageOutputQuality":
				in = depth
			case t.Name.Local == "Option" && in >= 0 && depth == in+1:
				if q, ok := byName[name]; ok {
					add(q)
				}
			}
		case xml.EndElement:
			if depth == in {
				in = -1
			}
			depth--
		}
	}
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func localName(qname string) string {
	_, local, ok := strings.Cut(qname, ":")
	if !ok {
		return qname
	}
	return local
}
