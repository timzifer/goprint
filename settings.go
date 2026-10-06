package goprint

// Settings are print settings. Zero values mean "printer default".
type Settings struct {
	// Printer is the queue or printer name; empty means the default printer.
	Printer string
	// Copies is the number of copies; 0 means 1.
	Copies int
	// Collate requests collated copies; nil means printer default.
	Collate *bool
	// PageRanges selects pages (1-based, inclusive); empty means all pages.
	PageRanges  []PageRange
	Media       Media
	Orientation Orientation
	Duplex      Duplex
	Color       ColorMode
	Quality     Quality
	Scaling     Scaling
	// Tray is the media source, e.g. "tray-1" or "manual".
	Tray string
	// Vendor carries platform-specific values verbatim: IPP attributes on
	// Unix and macOS, PrintTicket features on Windows.
	Vendor map[string]string

	// Strict turns warnings about unsupported settings into errors.
	Strict bool
	// Credentials authenticate against queues that require it (CUPS).
	Credentials *Credentials
}

// Credentials for authenticated print queues.
type Credentials struct {
	Username string
	Password string
}

// PageRange is an inclusive, 1-based page range. To == 0 means "to the end".
type PageRange struct {
	From, To int
}

// Orientation of the content on the page.
type Orientation int

const (
	OrientationDefault Orientation = iota
	Portrait
	Landscape
	ReversePortrait
	ReverseLandscape
)

// Duplex mode.
type Duplex int

const (
	DuplexDefault Duplex = iota
	DuplexNone
	DuplexLongEdge
	DuplexShortEdge
)

// ColorMode selects color or monochrome output.
type ColorMode int

const (
	ColorAuto ColorMode = iota
	Color
	Monochrome
)

// Quality is the print quality.
type Quality int

const (
	QualityDefault Quality = iota
	QualityDraft
	QualityNormal
	QualityHigh
)

// Scaling controls how pages are fitted to the media.
type Scaling int

const (
	ScalingDefault Scaling = iota
	ScalingFit
	ScalingFill
	ScalingNone
)

func (s Settings) validate() error {
	if s.Copies < 0 {
		return invalidf("settings: negative copies %d", s.Copies)
	}
	for _, r := range s.PageRanges {
		if r.From < 1 || (r.To != 0 && r.To < r.From) {
			return invalidf("settings: invalid page range %d-%d", r.From, r.To)
		}
	}
	if s.Media.Name != "" {
		if _, err := ParseMedia(s.Media.Name); err != nil {
			return err
		}
	}
	return nil
}
