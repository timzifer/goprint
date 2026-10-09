package virtualprinter

import "github.com/timzifer/goprint"

// Media of the presets that goprint has no variable for.
var (
	// MediaLabel62x100 is a 62 × 100 mm label.
	MediaLabel62x100 = goprint.Media{Name: "om_label_62x100mm", Width: 62000, Height: 100000}
	// MediaPhoto4x6 is a 4 × 6 in (10 × 15 cm) photo.
	MediaPhoto4x6 = goprint.Media{Name: "na_index-4x6_4x6in", Width: 101600, Height: 152400}
	// MediaReceipt80 is an 80 mm receipt roll, cut at 297 mm.
	MediaReceipt80 = goprint.Media{Name: "om_receipt_80x297mm", Width: 80000, Height: 297000}
)

var pdfOnly = []string{"application/pdf"}

// Office is a color laser printer with duplex, three paper sources and
// A3, A4, A5, Letter and Legal.
func Office(name string) Printer {
	return Printer{
		Name:        name,
		Description: "Virtual office printer",
		Caps: goprint.Capabilities{
			Media:       []goprint.Media{goprint.MediaA4, goprint.MediaA3, goprint.MediaA5, goprint.MediaLetter, goprint.MediaLegal},
			Duplex:      true,
			Color:       true,
			Resolutions: []goprint.Resolution{{X: 600, Y: 600}, {X: 1200, Y: 1200}},
			Formats:     pdfOnly,
			Trays:       []string{"auto", "tray-1", "manual"},
			Qualities:   []goprint.Quality{goprint.QualityDraft, goprint.QualityNormal, goprint.QualityHigh},
		},
	}
}

// Label is a monochrome label printer for 62 × 100 mm labels.
func Label(name string) Printer {
	return Printer{
		Name:        name,
		Description: "Virtual label printer",
		Caps: goprint.Capabilities{
			Media:       []goprint.Media{MediaLabel62x100},
			Resolutions: []goprint.Resolution{{X: 300, Y: 300}},
			Formats:     pdfOnly,
			Qualities:   []goprint.Quality{goprint.QualityNormal},
		},
	}
}

// Photo is a color photo printer for 4 × 6 in photos and A4.
func Photo(name string) Printer {
	return Printer{
		Name:        name,
		Description: "Virtual photo printer",
		Caps: goprint.Capabilities{
			Media:       []goprint.Media{MediaPhoto4x6, goprint.MediaA4},
			Color:       true,
			Resolutions: []goprint.Resolution{{X: 1200, Y: 1200}, {X: 4800, Y: 1200}},
			Formats:     pdfOnly,
			Trays:       []string{"auto", "photo"},
			Qualities:   []goprint.Quality{goprint.QualityNormal, goprint.QualityHigh},
		},
	}
}

// Receipt is a monochrome receipt printer on 80 mm paper rolls.
func Receipt(name string) Printer {
	return Printer{
		Name:        name,
		Description: "Virtual receipt printer",
		Caps: goprint.Capabilities{
			Media:       []goprint.Media{MediaReceipt80},
			Resolutions: []goprint.Resolution{{X: 203, Y: 203}},
			Formats:     pdfOnly,
			Qualities:   []goprint.Quality{goprint.QualityDraft},
		},
	}
}

// PDFWriter is a printer that writes files, like "Microsoft Print to PDF"
// or CUPS-PDF.
func PDFWriter(name string) Printer {
	p := Office(name)
	p.Description = "Virtual PDF writer"
	p.ToFile = true
	p.Caps.Trays = nil
	p.Caps.Resolutions = []goprint.Resolution{{X: 600, Y: 600}}
	return p
}
