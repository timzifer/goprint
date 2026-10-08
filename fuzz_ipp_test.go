package goprint

import (
	"bytes"
	"testing"

	"github.com/timzifer/goprint/ipp"
)

// FuzzCapsFromIPP feeds arbitrary IPP responses (as a printer could send
// them) through the capability mapping.
func FuzzCapsFromIPP(f *testing.F) {
	m := ipp.NewResponse(ipp.StatusOK, 1)
	m.AddGroup(ipp.TagPrinterGroup).Attrs = officeAttrs
	b, _ := m.MarshalBinary()
	f.Add(b)
	// Values of unexpected, uncomparable types (#28).
	m = ipp.NewResponse(ipp.StatusOK, 1)
	m.AddGroup(ipp.TagPrinterGroup).Attrs = ipp.Attributes{
		{Name: "print-quality-supported", Values: []ipp.Value{ipp.OctetString("x"), ipp.Enum(4)}},
		{Name: "media-source-supported", Values: []ipp.Value{ipp.OctetString("tray-1")}},
	}
	b, _ = m.MarshalBinary()
	f.Add(b)
	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := ipp.Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		for _, g := range msg.GroupsByTag(ipp.TagPrinterGroup) {
			c := capsFromIPP(g.Attrs)
			for _, m := range c.Media {
				if m.Width <= 0 || m.Height <= 0 {
					t.Fatalf("media with non-positive size: %+v", m)
				}
			}
			_ = printerFromIPP(ipp.Printer{Attrs: g.Attrs}, "")
		}
	})
}
