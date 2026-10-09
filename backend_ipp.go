package goprint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/timzifer/goprint/ipp"
)

// ippBackend prints through IPP: to the local CUPS scheduler on Linux, BSD
// and macOS, and to IPP Everywhere printers addressed by URI on every
// platform.
type ippBackend struct {
	// newClient connects to the default server (CUPS).
	newClient func(opts ...ipp.Option) (*ipp.Client, error)
}

// discoveryTimeout bounds discovery and job submission when the caller's
// context has no deadline.
const discoveryTimeout = 30 * time.Second

func withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, discoveryTimeout)
}

// isPrinterURI reports whether a printer name is an IPP URI rather than a
// queue name.
func isPrinterURI(name string) bool {
	return strings.HasPrefix(name, "ipp://") || strings.HasPrefix(name, "ipps://")
}

// client returns a client for printer: the printer's own server if it is a
// URI, the default server otherwise.
func (b ippBackend) client(printer string, creds *Credentials) (*ipp.Client, error) {
	var opts []ipp.Option
	if creds != nil {
		opts = append(opts, ipp.WithCredentials(creds.Username, creds.Password))
	}
	if isPrinterURI(printer) {
		return ipp.NewClient(printer, opts...)
	}
	return b.newClient(opts...)
}

// printerAttrs are requested for discovery and capabilities.
var printerAttrs = []string{
	"printer-name", "printer-info", "printer-location", "printer-uri-supported",
	"printer-state", "printer-state-reasons",
	"media-supported", "sides-supported", "color-supported", "print-color-mode-supported",
	"printer-resolution-supported", "document-format-supported",
	"media-source-supported", "print-quality-supported", "device-uri",
}

func (b ippBackend) printers(ctx context.Context) ([]Printer, error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	c, err := b.newClient()
	if err != nil {
		return nil, err
	}
	ps, err := c.CUPSGetPrinters(ctx, printerAttrs...)
	if err != nil {
		return nil, ippError(err, "")
	}
	def := ""
	if d, err := c.CUPSGetDefault(ctx, "printer-name"); err == nil {
		def = d.Name
	}
	out := make([]Printer, 0, len(ps))
	for _, p := range ps {
		out = append(out, printerFromIPP(p, def))
	}
	return out, nil
}

func printerFromIPP(p ipp.Printer, def string) Printer {
	str := func(name string) string {
		if a, ok := p.Attrs.Get(name); ok {
			return a.String()
		}
		return ""
	}
	return Printer{
		Name:        p.Name,
		Description: str("printer-info"),
		Location:    str("printer-location"),
		Default:     p.Name != "" && p.Name == def,
		ToFile:      fileOutputName(p.Name) || fileDeviceURI(str("device-uri")),
		Caps:        capsFromIPP(p.Attrs),
	}
}

// qualityFromIPP maps print-quality enums (RFC 8011 5.2.13). Values come
// from the printer and may have any type.
func qualityFromIPP(v ipp.Value) (Quality, bool) {
	switch v {
	case ipp.Enum(3):
		return QualityDraft, true
	case ipp.Enum(4):
		return QualityNormal, true
	case ipp.Enum(5):
		return QualityHigh, true
	}
	return QualityDefault, false
}

// fileDeviceURI reports CUPS backends that write files.
func fileDeviceURI(uri string) bool {
	return strings.HasPrefix(uri, "cups-pdf:") || strings.HasPrefix(uri, "file:")
}

func capsFromIPP(attrs ipp.Attributes) Capabilities {
	var c Capabilities
	if a, ok := attrs.Get("media-supported"); ok {
		for _, name := range a.Strings() {
			if m, err := ParseMedia(name); err == nil {
				c.Media = append(c.Media, m)
			}
		}
	}
	if a, ok := attrs.Get("sides-supported"); ok {
		for _, s := range a.Strings() {
			if strings.HasPrefix(s, "two-sided") {
				c.Duplex = true
			}
		}
	}
	if a, ok := attrs.Get("color-supported"); ok {
		c.Color, _ = a.Bool()
	}
	if a, ok := attrs.Get("print-color-mode-supported"); ok {
		for _, s := range a.Strings() {
			if s == "color" {
				c.Color = true
			}
		}
	}
	if a, ok := attrs.Get("printer-resolution-supported"); ok {
		for _, v := range a.Values {
			if r, ok := v.(ipp.Resolution); ok {
				x, y := int(r.X), int(r.Y)
				if r.Units == ipp.UnitsDPCM {
					x, y = int(math.Round(float64(x)*2.54)), int(math.Round(float64(y)*2.54))
				}
				c.Resolutions = append(c.Resolutions, Resolution{X: x, Y: y})
			}
		}
	}
	if a, ok := attrs.Get("document-format-supported"); ok {
		c.Formats = a.Strings()
	}
	if a, ok := attrs.Get("media-source-supported"); ok {
		c.Trays = a.Strings()
	}
	if a, ok := attrs.Get("print-quality-supported"); ok {
		for _, v := range a.Values {
			if q, ok := qualityFromIPP(v); ok && !slices.Contains(c.Qualities, q) {
				c.Qualities = append(c.Qualities, q)
			}
		}
	}
	return c
}

func (b ippBackend) capabilities(ctx context.Context, printer string) (Capabilities, error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	c, name, err := b.resolve(ctx, printer, nil)
	if err != nil {
		return Capabilities{}, err
	}
	p, err := c.GetPrinterAttributes(ctx, name, printerAttrs...)
	if err != nil {
		return Capabilities{}, ippError(err, name)
	}
	return capsFromIPP(p.Attrs), nil
}

// resolve returns the client and printer (queue name or URI) to use;
// an empty name means the CUPS default printer.
func (b ippBackend) resolve(ctx context.Context, printer string, creds *Credentials) (*ipp.Client, string, error) {
	c, err := b.client(printer, creds)
	if err != nil {
		return nil, "", err
	}
	if printer != "" {
		return c, printer, nil
	}
	d, err := c.CUPSGetDefault(ctx, "printer-name")
	if err != nil {
		var se *ipp.StatusError
		if errors.As(err, &se) && se.Code == ipp.StatusErrorNotFound {
			return nil, "", fmt.Errorf("%w: no default printer", ErrNoPrinter)
		}
		return nil, "", ippError(err, "")
	}
	return c, d.Name, nil
}

func (b ippBackend) print(ctx context.Context, src io.Reader, doc Document, s Settings) (*Job, error) {
	return b.printFormat(ctx, src, doc, s, "", nil)
}

// printFormat is print with the document-format (empty: PDF) and the
// warnings found before.
func (b ippBackend) printFormat(ctx context.Context, src io.Reader, doc Document, s Settings, format string, warnings []Warning) (*Job, error) {
	submitCtx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	c, name, err := b.resolve(submitCtx, s.Printer, s.Credentials)
	if err != nil {
		return nil, err
	}
	attrs, w := ippJobAttributes(s)
	warnings = append(warnings, w...)
	opts := &ipp.PrintJobOptions{JobName: doc.Title, Job: attrs, DocumentFormat: format}
	if s.Strict {
		// Ask the printer to reject jobs it cannot print as requested.
		opts.Operation = ipp.Attributes{{Name: "ipp-attribute-fidelity", Values: []ipp.Value{ipp.Boolean(true)}}}
		if len(warnings) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
		}
	}
	j, err := c.PrintJob(submitCtx, name, src, opts)
	if err != nil {
		return nil, ippError(err, name)
	}
	for _, a := range j.Unsupported {
		warnings = append(warnings, Warning{settingForIPP(a.Name), fmt.Sprintf("printer ignored or substituted %s", a.Name)})
	}
	job := &ippJob{c: c, printer: name, jobID: j.ID}
	if s.Strict && len(j.Unsupported) > 0 {
		_ = c.CancelJob(submitCtx, name, j.ID)
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, warnings[len(warnings)-1])
	}
	return &Job{b: job, warnings: warnings}, nil
}

func (ippBackend) dialog(context.Context, Document, DialogOptions) (*Job, Settings, error) {
	// TODO(phase 4): xdg-desktop-portal on Linux, NSPrintOperation on macOS.
	return nil, Settings{}, fmt.Errorf("%w: not implemented on this platform yet", ErrNoDialog)
}

func (ippBackend) properties(context.Context, Settings, uintptr) (Settings, error) {
	return Settings{}, fmt.Errorf("%w: printers have no driver dialog on this platform", ErrUnsupported)
}

// ippJobAttributes maps settings to IPP job template attributes. It reports
// settings that cannot be expressed.
func ippJobAttributes(s Settings) (ipp.Attributes, []Warning) {
	var a ipp.Attributes
	var w []Warning

	if s.Copies > 1 {
		a.Add("copies", ipp.Integer(s.Copies))
	}
	if s.Collate != nil {
		v := "separate-documents-uncollated-copies"
		if *s.Collate {
			v = "separate-documents-collated-copies"
		}
		a.Add("multiple-document-handling", ipp.Keyword(v))
	}
	if len(s.PageRanges) > 0 {
		var rs []ipp.Value
		for _, r := range s.PageRanges {
			to := r.To
			if to == 0 {
				to = math.MaxInt32
			}
			rs = append(rs, ipp.Range{Lower: int32(r.From), Upper: int32(to)})
		}
		a.Add("page-ranges", rs...)
	}

	// Media: a plain keyword when possible, media-col for custom sizes and
	// trays.
	customSize := s.Media.Name == "" && s.Media.Width > 0 && s.Media.Height > 0
	switch {
	case customSize || s.Tray != "":
		var col ipp.Collection
		if customSize {
			col = append(col, ipp.Attribute{Name: "media-size", Values: []ipp.Value{ipp.Collection{
				{Name: "x-dimension", Values: []ipp.Value{ipp.Integer(s.Media.Width / 10)}},
				{Name: "y-dimension", Values: []ipp.Value{ipp.Integer(s.Media.Height / 10)}},
			}}})
		} else if s.Media.Name != "" {
			col = append(col, ipp.Attribute{Name: "media-size-name", Values: []ipp.Value{ipp.Keyword(s.Media.Name)}})
		}
		if s.Tray != "" {
			col = append(col, ipp.Attribute{Name: "media-source", Values: []ipp.Value{ipp.Keyword(s.Tray)}})
		}
		a.Add("media-col", col)
	case s.Media.Name != "":
		a.Add("media", ipp.Keyword(s.Media.Name))
	}

	switch s.Orientation {
	case Portrait:
		a.Add("orientation-requested", ipp.Enum(3))
	case Landscape:
		a.Add("orientation-requested", ipp.Enum(4))
	case ReverseLandscape:
		a.Add("orientation-requested", ipp.Enum(5))
	case ReversePortrait:
		a.Add("orientation-requested", ipp.Enum(6))
	}
	switch s.Duplex {
	case DuplexNone:
		a.Add("sides", ipp.Keyword("one-sided"))
	case DuplexLongEdge:
		a.Add("sides", ipp.Keyword("two-sided-long-edge"))
	case DuplexShortEdge:
		a.Add("sides", ipp.Keyword("two-sided-short-edge"))
	}
	switch s.Color {
	case Color:
		a.Add("print-color-mode", ipp.Keyword("color"))
	case Monochrome:
		a.Add("print-color-mode", ipp.Keyword("monochrome"))
	}
	switch s.Quality {
	case QualityDraft:
		a.Add("print-quality", ipp.Enum(3))
	case QualityNormal:
		a.Add("print-quality", ipp.Enum(4))
	case QualityHigh:
		a.Add("print-quality", ipp.Enum(5))
	}
	switch s.Scaling {
	case ScalingFit:
		a.Add("print-scaling", ipp.Keyword("fit"))
	case ScalingFill:
		a.Add("print-scaling", ipp.Keyword("fill"))
	case ScalingNone:
		a.Add("print-scaling", ipp.Keyword("none"))
	}
	for k, v := range s.Vendor {
		if strings.Contains(k, ":") {
			// Namespaced keys belong to other backends (e.g. "windows:...").
			w = append(w, Warning{"Vendor[" + k + "]", "not an IPP attribute"})
			continue
		}
		a.Set(k, vendorValue(v))
	}
	return a, w
}

// vendorValue turns a Vendor string into an IPP value: integers and
// booleans keep their type, everything else is sent as keyword.
func vendorValue(v string) ipp.Value {
	if n, err := strconv.ParseInt(v, 10, 32); err == nil {
		return ipp.Integer(n)
	}
	switch v {
	case "true":
		return ipp.Boolean(true)
	case "false":
		return ipp.Boolean(false)
	}
	return ipp.Keyword(v)
}

// settingForIPP names the Settings field behind an IPP attribute.
func settingForIPP(attr string) string {
	switch attr {
	case "copies":
		return "Copies"
	case "multiple-document-handling":
		return "Collate"
	case "page-ranges":
		return "PageRanges"
	case "media", "media-col":
		return "Media"
	case "orientation-requested":
		return "Orientation"
	case "sides":
		return "Duplex"
	case "print-color-mode":
		return "Color"
	case "print-quality":
		return "Quality"
	case "print-scaling":
		return "Scaling"
	}
	return "Vendor[" + attr + "]"
}

// ippError maps IPP failures to goprint sentinels, keeping the details.
func ippError(err error, printer string) error {
	var se *ipp.StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case ipp.StatusErrorNotFound:
			if printer != "" {
				return fmt.Errorf("%w: %q: %w", ErrPrinterNotFound, printer, err)
			}
		case ipp.StatusErrorAttributesOrValues, ipp.StatusErrorDocumentFormat, ipp.StatusErrorConflicting:
			return fmt.Errorf("%w: %w", ErrUnsupported, err)
		case ipp.StatusErrorBusy, ipp.StatusErrorServiceUnavailable, ipp.StatusErrorNotAcceptingJobs:
			return fmt.Errorf("%w: %w", ErrBusy, err)
		}
	}
	return err
}

// ippJob tracks a job via Get-Job-Attributes.
type ippJob struct {
	c       *ipp.Client
	printer string
	jobID   int
}

func (j *ippJob) id() string { return strconv.Itoa(j.jobID) }

func (j *ippJob) state(ctx context.Context) (JobState, error) {
	r, err := j.c.GetJobAttributes(ctx, j.printer, j.jobID, "job-state", "job-state-reasons")
	if err != nil {
		return JobPending, ippError(err, j.printer)
	}
	switch r.State {
	case ipp.JobProcessing, ipp.JobProcessingStopped:
		return JobProcessing, nil
	case ipp.JobCanceled:
		return JobCanceled, nil
	case ipp.JobAborted:
		return JobAborted, nil
	case ipp.JobCompleted:
		return JobCompleted, nil
	}
	return JobPending, nil
}

func (j *ippJob) wait(ctx context.Context) error {
	delay := 100 * time.Millisecond
	for {
		st, err := j.state(ctx)
		if err != nil {
			return err
		}
		switch st {
		case JobCompleted:
			return nil
		case JobCanceled:
			return ErrCanceled
		case JobAborted:
			return fmt.Errorf("goprint: job %d on %s aborted", j.jobID, j.printer)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

func (j *ippJob) cancel(ctx context.Context) error {
	return ippError(j.c.CancelJob(ctx, j.printer, j.jobID), j.printer)
}
