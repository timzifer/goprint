// Command printdemo exercises goprint by hand: list printers, show
// capabilities, open the native print dialog, or print headless.
//
//	go run ./examples/printdemo -mode list
//	go run ./examples/printdemo -mode caps -printer NAME
//	go run ./examples/printdemo -mode dialog                 # dialog, print from it
//	go run ./examples/printdemo -mode settings               # dialog, settings only
//	go run ./examples/printdemo -mode print -printer NAME    # headless
//
// Without -pdf a 5-page A4 test document is used. Headless printing needs
// an explicit -printer so that nothing reaches the default printer by
// accident.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/testpdf"
)

// AppKit (macOS) needs the main thread for the print panel.
func init() { runtime.LockOSThread() }

func main() {
	var (
		mode      = flag.String("mode", "list", "list | caps | dialog | settings | print")
		printer   = flag.String("printer", "", "printer (queue) name; required for -mode print")
		pdfPath   = flag.String("pdf", "", "PDF to print (default: generated 5-page A4 test document)")
		copies    = flag.Int("copies", 0, "copies preset")
		landscape = flag.Bool("landscape", false, "landscape preset")
		media     = flag.String("media", "", "PWG media preset, e.g. iso_a5_148x210mm")
		pages     = flag.String("pages", "", "page range preset, e.g. 2-3")
		duplex    = flag.String("duplex", "", "none | long | short")
		mono      = flag.Bool("mono", false, "monochrome preset")
		require   = flag.Bool("require-printer", false, "DialogOptions.RequirePrinter")
		timeout   = flag.Duration("timeout", 10*time.Minute, "overall timeout")
	)
	flag.Parse()

	s := goprint.Settings{Printer: *printer, Copies: *copies}
	if *landscape {
		s.Orientation = goprint.Landscape
	}
	if *media != "" {
		m, err := goprint.ParseMedia(*media)
		if err != nil {
			fail(err)
		}
		s.Media = m
	}
	if *pages != "" {
		r, err := parseRange(*pages)
		if err != nil {
			fail(err)
		}
		s.PageRanges = []goprint.PageRange{r}
	}
	switch *duplex {
	case "":
	case "none":
		s.Duplex = goprint.DuplexNone
	case "long":
		s.Duplex = goprint.DuplexLongEdge
	case "short":
		s.Duplex = goprint.DuplexShortEdge
	default:
		fail(fmt.Errorf("unknown -duplex %q", *duplex))
	}
	if *mono {
		s.Color = goprint.Monochrome
	}

	doc, err := document(*pdfPath)
	if err != nil {
		fail(err)
	}

	var runErr error
	goprint.RunMain(func() {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		runErr = run(ctx, *mode, doc, s, *require)
	})
	if runErr != nil {
		fail(runErr)
	}
}

func run(ctx context.Context, mode string, doc goprint.Document, s goprint.Settings, require bool) error {
	switch mode {
	case "list":
		ps, err := goprint.Printers(ctx)
		if err != nil {
			return err
		}
		for _, p := range ps {
			def := " "
			if p.Default {
				def = "*"
			}
			fmt.Printf("%s %-40s %s %s\n", def, p.Name, p.Description, p.Location)
		}
		return nil

	case "caps":
		c, err := goprint.GetCapabilities(ctx, s.Printer)
		if err != nil {
			return err
		}
		fmt.Printf("duplex %v, color %v, preview in dialog %v\n", c.Duplex, c.Color, c.DialogPreview)
		fmt.Printf("resolutions %v\nformats %v\n", c.Resolutions, c.Formats)
		for _, m := range c.Media {
			fmt.Printf("  %s (%.1f x %.1f mm)\n", m.Name, float64(m.Width)/1000, float64(m.Height)/1000)
		}
		return nil

	case "dialog", "settings":
		job, chosen, err := goprint.Dialog(ctx, doc, goprint.DialogOptions{
			Settings:       s,
			RequirePrinter: require,
			PrintNow:       mode == "dialog",
		})
		if errors.Is(err, goprint.ErrCanceled) {
			fmt.Println("canceled")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("chosen settings:\n%s", describe(chosen))
		if job == nil {
			fmt.Println("no job (settings only)")
			return nil
		}
		return follow(ctx, job)

	case "print":
		if s.Printer == "" {
			return errors.New("-mode print needs -printer (no silent printing to the default printer)")
		}
		job, err := goprint.Print(ctx, doc, s)
		if err != nil {
			return err
		}
		return follow(ctx, job)
	}
	return fmt.Errorf("unknown -mode %q", mode)
}

func follow(ctx context.Context, job *goprint.Job) error {
	fmt.Printf("job %q\n", job.ID())
	for _, w := range job.Warnings() {
		fmt.Printf("warning: %s\n", w)
	}
	if err := job.Wait(ctx); err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	st, _ := job.State(ctx)
	fmt.Printf("job state: %v\n", st)
	return nil
}

func describe(s goprint.Settings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  printer     %q\n", s.Printer)
	fmt.Fprintf(&b, "  copies      %d\n", s.Copies)
	if s.Collate != nil {
		fmt.Fprintf(&b, "  collate     %v\n", *s.Collate)
	}
	fmt.Fprintf(&b, "  pages       %v\n", s.PageRanges)
	fmt.Fprintf(&b, "  media       %s\n", s.Media)
	fmt.Fprintf(&b, "  orientation %d (1 portrait, 2 landscape)\n", s.Orientation)
	fmt.Fprintf(&b, "  duplex      %d (1 none, 2 long, 3 short)\n", s.Duplex)
	fmt.Fprintf(&b, "  color       %d (1 color, 2 monochrome)\n", s.Color)
	if s.Tray != "" {
		fmt.Fprintf(&b, "  tray        %s\n", s.Tray)
	}
	return b.String()
}

func document(path string) (goprint.Document, error) {
	var data []byte
	title := "goprint demo"
	if path == "" {
		data = testpdf.Generate(5, testpdf.A4Width, testpdf.A4Height)
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return goprint.Document{}, err
		}
		data, title = b, path
	}
	return goprint.Document{
		Title: title,
		PDF:   func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(data)}, nil },
	}, nil
}

type nopCloser struct{ io.ReadSeeker }

func (nopCloser) Close() error { return nil }

func parseRange(s string) (goprint.PageRange, error) {
	a, b, ok := strings.Cut(s, "-")
	from, err := strconv.Atoi(a)
	if err != nil {
		return goprint.PageRange{}, fmt.Errorf("bad -pages %q", s)
	}
	to := from
	if ok {
		if b == "" {
			to = 0
		} else if to, err = strconv.Atoi(b); err != nil {
			return goprint.PageRange{}, fmt.Errorf("bad -pages %q", s)
		}
	}
	return goprint.PageRange{From: from, To: to}, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "printdemo:", err)
	os.Exit(1)
}
