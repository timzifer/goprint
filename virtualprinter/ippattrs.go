package virtualprinter

import (
	"math"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/ipp"
)

// settingsFromIPP maps job template attributes onto settings, the inverse
// of what goprint's IPP backend sends. Values it cannot read are returned
// as unsupported; attributes it does not know go to Settings.Vendor.
func settingsFromIPP(attrs ipp.Attributes) (goprint.Settings, ipp.Attributes) {
	var s goprint.Settings
	var unsupported ipp.Attributes
	bad := func(a ipp.Attribute) { unsupported = append(unsupported, a) }
	for _, a := range attrs {
		kw := ""
		if len(a.Values) > 0 {
			kw = a.Strings()[0]
		}
		switch a.Name {
		case "copies":
			if n, ok := a.Int(); ok && n >= 1 {
				s.Copies = n
			} else {
				bad(a)
			}
		case "multiple-document-handling":
			switch kw {
			case "separate-documents-collated-copies":
				s.Collate = new(bool)
				*s.Collate = true
			case "separate-documents-uncollated-copies":
				s.Collate = new(bool)
			default:
				bad(a)
			}
		case "page-ranges":
			for _, v := range a.Values {
				r, ok := v.(ipp.Range)
				if !ok || r.Lower < 1 || r.Upper < r.Lower {
					bad(a)
					s.PageRanges = nil
					break
				}
				to := int(r.Upper)
				if r.Upper == math.MaxInt32 {
					to = 0
				}
				s.PageRanges = append(s.PageRanges, goprint.PageRange{From: int(r.Lower), To: to})
			}
		case "media":
			if m, err := goprint.ParseMedia(kw); err == nil {
				s.Media = m
			} else {
				bad(a)
			}
		case "media-col":
			col, ok := a.Values[0].(ipp.Collection)
			if !ok {
				bad(a)
				continue
			}
			if !mediaFromCol(&s, col) {
				bad(a)
			}
		case "media-source":
			s.Tray = kw
		case "orientation-requested":
			switch n, _ := a.Int(); n {
			case 3:
				s.Orientation = goprint.Portrait
			case 4:
				s.Orientation = goprint.Landscape
			case 5:
				s.Orientation = goprint.ReverseLandscape
			case 6:
				s.Orientation = goprint.ReversePortrait
			default:
				bad(a)
			}
		case "sides":
			switch kw {
			case "one-sided":
				s.Duplex = goprint.DuplexNone
			case "two-sided-long-edge":
				s.Duplex = goprint.DuplexLongEdge
			case "two-sided-short-edge":
				s.Duplex = goprint.DuplexShortEdge
			default:
				bad(a)
			}
		case "print-color-mode":
			switch kw {
			case "color":
				s.Color = goprint.Color
			case "monochrome":
				s.Color = goprint.Monochrome
			case "auto":
			default:
				bad(a)
			}
		case "print-quality":
			switch n, _ := a.Int(); n {
			case 3:
				s.Quality = goprint.QualityDraft
			case 4:
				s.Quality = goprint.QualityNormal
			case 5:
				s.Quality = goprint.QualityHigh
			default:
				bad(a)
			}
		case "print-scaling":
			switch kw {
			case "fit":
				s.Scaling = goprint.ScalingFit
			case "fill":
				s.Scaling = goprint.ScalingFill
			case "none":
				s.Scaling = goprint.ScalingNone
			case "auto", "auto-fit":
			default:
				bad(a)
			}
		default:
			if s.Vendor == nil {
				s.Vendor = map[string]string{}
			}
			s.Vendor[a.Name] = kw
		}
	}
	return s, unsupported
}

// mediaFromCol reads media-size-name, media-size and media-source of a
// media-col.
func mediaFromCol(s *goprint.Settings, col ipp.Collection) bool {
	if a, found := ipp.Attributes(col).Get("media-size-name"); found {
		m, err := goprint.ParseMedia(a.String())
		if err != nil {
			return false
		}
		s.Media = m
	}
	if a, found := ipp.Attributes(col).Get("media-size"); found && s.Media.Name == "" {
		size, isCol := a.Values[0].(ipp.Collection)
		if !isCol {
			return false
		}
		x, _ := ipp.Attributes(size).Get("x-dimension")
		y, _ := ipp.Attributes(size).Get("y-dimension")
		w, okW := x.Int()
		h, okH := y.Int()
		if !okW || !okH || w <= 0 || h <= 0 {
			return false
		}
		s.Media = goprint.Media{Width: w * 10, Height: h * 10} // hundredths of mm
	}
	if a, found := ipp.Attributes(col).Get("media-source"); found {
		s.Tray = a.String()
	}
	return true
}

// operations are the IPP operations of the server.
var operations = []ipp.Operation{
	ipp.OpPrintJob, ipp.OpValidateJob, ipp.OpCreateJob, ipp.OpSendDocument, ipp.OpCancelJob,
	ipp.OpGetJobAttributes, ipp.OpGetJobs, ipp.OpGetPrinterAttributes,
}

// printerAttributes describes pr in IPP printer attributes.
func printerAttributes(pr Printer, offline bool, host string) ipp.Attributes {
	var as ipp.Attributes
	add := func(name string, vs ...ipp.Value) {
		if len(vs) > 0 {
			as = append(as, ipp.Attribute{Name: name, Values: vs})
		}
	}
	keywords := func(ks ...string) []ipp.Value {
		vs := make([]ipp.Value, len(ks))
		for i, k := range ks {
			vs[i] = ipp.Keyword(k)
		}
		return vs
	}
	c := pr.Caps
	info := pr.Description
	if info == "" {
		info = pr.Name
	}
	add("printer-uri-supported", ipp.URI("ipp://"+host+printerPath(pr.Name)))
	add("uri-security-supported", ipp.Keyword("none"))
	add("uri-authentication-supported", ipp.Keyword("none"))
	add("printer-name", ipp.Name(pr.Name))
	add("printer-info", ipp.Text(info))
	add("printer-location", ipp.Text(pr.Location))
	add("printer-make-and-model", ipp.Text("goprint virtual printer"))
	if offline {
		add("printer-state", ipp.Enum(ipp.PrinterStopped))
		add("printer-state-reasons", ipp.Keyword("offline-report"))
	} else {
		add("printer-state", ipp.Enum(ipp.PrinterIdle))
		add("printer-state-reasons", ipp.Keyword("none"))
	}
	add("printer-is-accepting-jobs", ipp.Boolean(!offline))
	var ops []ipp.Value
	for _, o := range operations {
		ops = append(ops, ipp.Enum(o))
	}
	add("operations-supported", ops...)
	add("charset-configured", ipp.Charset("utf-8"))
	add("charset-supported", ipp.Charset("utf-8"))
	add("natural-language-configured", ipp.NaturalLanguage("en"))
	add("generated-natural-language-supported", ipp.NaturalLanguage("en"))
	add("ipp-versions-supported", keywords("1.1", "2.0")...)
	add("document-format-default", ipp.MimeMediaType("application/pdf"))
	add("document-format-supported", ipp.MimeMediaType("application/pdf"), ipp.MimeMediaType("application/octet-stream"))
	add("pdl-override-supported", ipp.Keyword("attempted"))
	add("compression-supported", ipp.Keyword("none"))
	add("copies-supported", ipp.Range{Lower: 1, Upper: 999})
	add("copies-default", ipp.Integer(1))
	add("page-ranges-supported", ipp.Boolean(true))
	add("multiple-document-handling-supported", keywords("separate-documents-uncollated-copies", "separate-documents-collated-copies")...)
	add("orientation-requested-supported", ipp.Enum(3), ipp.Enum(4), ipp.Enum(5), ipp.Enum(6))
	add("print-scaling-supported", keywords("auto", "fit", "fill", "none")...)
	add("print-scaling-default", ipp.Keyword("auto"))

	var media []ipp.Value
	for _, m := range c.Media {
		if m.Name != "" {
			media = append(media, ipp.Keyword(m.Name))
		}
	}
	add("media-supported", media...)
	if len(media) > 0 {
		add("media-default", media[0])
		add("media-ready", media[0])
	}
	sides := keywords("one-sided")
	if c.Duplex {
		sides = append(sides, keywords("two-sided-long-edge", "two-sided-short-edge")...)
	}
	add("sides-supported", sides...)
	add("sides-default", ipp.Keyword("one-sided"))
	add("color-supported", ipp.Boolean(c.Color))
	if c.Color {
		add("print-color-mode-supported", keywords("auto", "monochrome", "color")...)
		add("print-color-mode-default", ipp.Keyword("color"))
	} else {
		add("print-color-mode-supported", keywords("auto", "monochrome")...)
		add("print-color-mode-default", ipp.Keyword("monochrome"))
	}
	var res []ipp.Value
	for _, r := range c.Resolutions {
		res = append(res, ipp.Resolution{X: int32(r.X), Y: int32(r.Y), Units: ipp.UnitsDPI})
	}
	add("printer-resolution-supported", res...)
	if len(res) > 0 {
		add("printer-resolution-default", res[0])
	}
	add("media-source-supported", keywords(c.Trays...)...)
	if len(c.Trays) > 0 {
		add("media-source-default", ipp.Keyword(c.Trays[0]))
	}
	var qs []ipp.Value
	for _, q := range c.Qualities {
		if n := map[goprint.Quality]int32{goprint.QualityDraft: 3, goprint.QualityNormal: 4, goprint.QualityHigh: 5}[q]; n > 0 {
			qs = append(qs, ipp.Enum(n))
		}
	}
	add("print-quality-supported", qs...)
	return as
}
