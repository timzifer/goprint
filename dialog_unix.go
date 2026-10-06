//go:build linux || freebsd || openbsd || netbsd || dragonfly

package goprint

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/timzifer/goprint/internal/portal"
)

// unixBackend prints headless through CUPS and shows the desktop's print
// dialog through xdg-desktop-portal.
type unixBackend struct {
	ippBackend
}

// VendorGTKPrefix marks Settings.Vendor keys passed to the print portal as
// GTK print settings, e.g. Vendor["gtk:output-bin"] = "top".
const VendorGTKPrefix = "gtk:"

func (b unixBackend) dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	if opts.Style == StyleClassic {
		return nil, Settings{}, fmt.Errorf("%w: StyleClassic is windows-only", ErrUnsupported)
	}
	ps, warnings := toPortalSettings(opts.Settings)
	if opts.RequirePrinter && opts.Settings.Printer == "" {
		warnings = append(warnings, Warning{"Printer", "RequirePrinter without Printer"})
	}
	if opts.Settings.Strict && len(warnings) > 0 {
		return nil, Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	c, err := portal.Connect()
	if err != nil {
		return nil, Settings{}, err
	}
	defer c.Close()

	parent := ""
	if opts.Owner != 0 {
		parent = fmt.Sprintf("x11:%x", opts.Owner)
	}
	title := doc.Title
	if title == "" {
		title = "Document"
	}
	res, err := c.PreparePrint(ctx, parent, title, ps)
	if err != nil {
		return nil, Settings{}, err
	}
	chosen := fromPortalSettings(res.Settings, opts.Settings)
	if opts.RequirePrinter && opts.Settings.Printer != "" && chosen.Printer != opts.Settings.Printer {
		// The portal implementation decides whether the preset printer is
		// honored; RequirePrinter must not print elsewhere.
		return nil, chosen, fmt.Errorf("%w: the print dialog did not keep printer %q (got %q)", ErrUnsupported, opts.Settings.Printer, chosen.Printer)
	}
	if !opts.PrintNow {
		return nil, chosen, nil
	}

	// The portal reads the document through a file descriptor.
	src, err := doc.open()
	if err != nil {
		return nil, Settings{}, err
	}
	defer src.Close()
	f, err := os.CreateTemp("", "goprint-*.pdf")
	if err != nil {
		return nil, Settings{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return nil, Settings{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, Settings{}, err
	}
	if err := c.Print(ctx, parent, title, f, res.Token); err != nil {
		return nil, Settings{}, err
	}
	return &Job{b: portalJob{}, warnings: warnings}, chosen, nil
}

// portalJob is a job handed to the print portal, which reports nothing
// about it afterwards.
type portalJob struct{}

func (portalJob) id() string { return "" }

func (portalJob) state(context.Context) (JobState, error) { return JobCompleted, nil }

func (portalJob) wait(context.Context) error { return nil }

func (portalJob) cancel(context.Context) error {
	return fmt.Errorf("%w: jobs printed through the portal cannot be canceled", ErrUnsupported)
}

// toPortalSettings maps settings onto GTK print settings and page setup.
func toPortalSettings(s Settings) (portal.Settings, []Warning) {
	p := portal.Settings{Print: map[string]string{}, PageSetup: map[string]dbus.Variant{}}
	var w []Warning
	set := func(k, v string) { p.Print[k] = v }

	if s.Printer != "" {
		set("printer", s.Printer)
	}
	if s.Copies > 0 {
		set("n-copies", strconv.Itoa(s.Copies))
	}
	if s.Collate != nil {
		set("collate", strconv.FormatBool(*s.Collate))
	}
	if len(s.PageRanges) > 0 {
		// GtkPageRange is 0-based; an open end becomes a large page number.
		var parts []string
		for _, r := range s.PageRanges {
			to := r.To
			if to == 0 {
				to = 1 << 30
			}
			parts = append(parts, fmt.Sprintf("%d-%d", r.From-1, to-1))
		}
		set("print-pages", "ranges")
		set("page-ranges", strings.Join(parts, ","))
	}
	orient := ""
	switch s.Orientation {
	case Portrait:
		orient = "portrait"
	case Landscape:
		orient = "landscape"
	case ReversePortrait:
		orient = "reverse_portrait"
	case ReverseLandscape:
		orient = "reverse_landscape"
	}
	if orient != "" {
		set("orientation", orient)
		p.PageSetup["Orientation"] = dbus.MakeVariant(orient)
	}
	switch s.Duplex {
	case DuplexNone:
		set("duplex", "simplex")
	case DuplexLongEdge:
		set("duplex", "horizontal") // GTK: horizontal = long edge (DuplexNoTumble)
	case DuplexShortEdge:
		set("duplex", "vertical")
	}
	switch s.Color {
	case Color:
		set("use-color", "true")
	case Monochrome:
		set("use-color", "false")
	}
	switch s.Quality {
	case QualityDraft:
		set("quality", "draft")
	case QualityNormal:
		set("quality", "normal")
	case QualityHigh:
		set("quality", "high")
	}
	switch s.Scaling {
	case ScalingNone:
		set("scale", "100")
	case ScalingFit, ScalingFill:
		w = append(w, Warning{"Scaling", "fit/fill cannot be preset through the print portal"})
	}
	if s.Tray != "" {
		set("default-source", s.Tray)
	}
	if s.Media != (Media{}) {
		m := s.Media
		if m.Width == 0 {
			m, _ = ParseMedia(m.Name)
		}
		if m.Name != "" {
			p.PageSetup["Name"] = dbus.MakeVariant(m.Name)
		}
		p.PageSetup["Width"] = dbus.MakeVariant(float64(m.Width) / 1000)
		p.PageSetup["Height"] = dbus.MakeVariant(float64(m.Height) / 1000)
	}
	if s.Credentials != nil {
		w = append(w, Warning{"Credentials", "not used by the print portal"})
	}
	for k, v := range s.Vendor {
		if after, ok := strings.CutPrefix(k, VendorGTKPrefix); ok {
			set(after, v)
		} else {
			w = append(w, Warning{"Vendor[" + k + "]", "only " + VendorGTKPrefix + " keys reach the print portal"})
		}
	}
	return p, w
}

// fromPortalSettings reads the user's choice back, starting from preset for
// what the portal does not report.
func fromPortalSettings(p portal.Settings, preset Settings) Settings {
	s := preset
	get := func(k string) (string, bool) { v, ok := p.Print[k]; return v, ok && v != "" }

	if v, ok := get("printer"); ok {
		s.Printer = v
	}
	if v, ok := get("n-copies"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.Copies = n
		}
	}
	if v, ok := get("collate"); ok {
		b := v == "true"
		s.Collate = &b
	}
	if v, ok := get("print-pages"); ok {
		s.PageRanges = nil
		if v == "ranges" {
			if r, ok := get("page-ranges"); ok {
				s.PageRanges = parseGTKRanges(r)
			}
		}
	}
	orient, _ := get("orientation")
	if v, ok := p.PageSetup["Orientation"]; ok {
		if o, ok := v.Value().(string); ok && o != "" {
			orient = o
		}
	}
	switch orient {
	case "portrait":
		s.Orientation = Portrait
	case "landscape":
		s.Orientation = Landscape
	case "reverse_portrait":
		s.Orientation = ReversePortrait
	case "reverse_landscape":
		s.Orientation = ReverseLandscape
	}
	switch v, _ := get("duplex"); v {
	case "simplex":
		s.Duplex = DuplexNone
	case "horizontal":
		s.Duplex = DuplexLongEdge
	case "vertical":
		s.Duplex = DuplexShortEdge
	}
	switch v, _ := get("use-color"); v {
	case "true":
		s.Color = Color
	case "false":
		s.Color = Monochrome
	}
	switch v, _ := get("quality"); v {
	case "draft", "low":
		s.Quality = QualityDraft
	case "normal":
		s.Quality = QualityNormal
	case "high":
		s.Quality = QualityHigh
	}
	if v, ok := get("default-source"); ok {
		s.Tray = v
	}
	w, wok := p.PageSetup["Width"].Value().(float64)
	h, hok := p.PageSetup["Height"].Value().(float64)
	if wok && hok && w > 0 && h > 0 {
		name, _ := p.PageSetup["Name"].Value().(string)
		if m, err := ParseMedia(name); err == nil && abs(m.Width-int(w*1000)) <= 1000 && abs(m.Height-int(h*1000)) <= 1000 {
			s.Media = m
		} else {
			display, _ := p.PageSetup["DisplayName"].Value().(string)
			s.Media = mediaBySize(int(w*1000+0.5), int(h*1000+0.5), display)
		}
	}
	return s
}

// parseGTKRanges parses GTK's 0-based "a-b,c" page ranges.
func parseGTKRanges(s string) []PageRange {
	var out []PageRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, b, isRange := strings.Cut(part, "-")
		from, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil {
			continue
		}
		to := from
		if isRange {
			if to, err = strconv.Atoi(strings.TrimSpace(b)); err != nil {
				continue
			}
		}
		r := PageRange{From: from + 1, To: to + 1}
		if to >= 1<<30-1 {
			r.To = 0
		}
		out = append(out, r)
	}
	return out
}
