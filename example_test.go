package goprint_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"

	"github.com/timzifer/goprint"
)

func ExamplePrinters() {
	ctx := context.Background()
	printers, err := goprint.Printers(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range printers {
		fmt.Printf("%s (default %v, duplex %v, color %v)\n", p.Name, p.Default, p.Caps.Duplex, p.Caps.Color)
	}
}

func ExamplePrint() {
	ctx := context.Background()
	job, err := goprint.Print(ctx, goprint.PDFFile("invoice.pdf"), goprint.Settings{
		Printer: "Office",
		Copies:  2,
		Media:   goprint.MediaA4,
		Duplex:  goprint.DuplexLongEdge,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, w := range job.Warnings() {
		log.Println("not honored:", w)
	}
	if err := job.Wait(ctx); err != nil {
		log.Fatal(err)
	}
}

func ExamplePrint_strict() {
	// With Strict, a setting the printer cannot honor fails the call
	// before anything is printed.
	_, err := goprint.Print(context.Background(), goprint.PDFFile("label.pdf"), goprint.Settings{
		Printer: "Label printer",
		Duplex:  goprint.DuplexLongEdge,
		Strict:  true,
	})
	if errors.Is(err, goprint.ErrUnsupported) {
		log.Println("printer cannot print two-sided:", err)
	}
}

func ExamplePrint_images() {
	// Raster pages, e.g. rendered by the application at 300 dpi.
	var pages []image.Image // one image per page
	doc := goprint.Document{Title: "Scan", Images: pages, DPI: 300}
	if _, err := goprint.Print(context.Background(), doc, goprint.Settings{Scaling: goprint.ScalingFit}); err != nil {
		log.Fatal(err)
	}
}

func ExampleDialog() {
	ctx := context.Background()
	job, chosen, err := goprint.Dialog(ctx, goprint.PDFFile("report.pdf"), goprint.DialogOptions{
		Settings: goprint.Settings{Copies: 2, Media: goprint.MediaA4},
		PrintNow: true,
	})
	switch {
	case errors.Is(err, goprint.ErrCanceled):
		return
	case errors.Is(err, goprint.ErrNoDialog):
		log.Fatal("no print dialog on this system")
	case err != nil:
		log.Fatal(err)
	}
	fmt.Println("printing on", chosen.Printer)
	if err := job.Wait(ctx); err != nil {
		log.Fatal(err)
	}
}

func ExampleDialog_settingsOnly() {
	// Ask once, print later (e.g. a "Page setup" command): without
	// PrintNow the dialog returns the chosen settings and no job.
	ctx := context.Background()
	doc := goprint.PDFFile("report.pdf")
	_, chosen, err := goprint.Dialog(ctx, doc, goprint.DialogOptions{})
	if err != nil {
		return
	}
	// ... later, without UI:
	if _, err := goprint.Print(ctx, doc, chosen); err != nil {
		log.Fatal(err)
	}
}

func ExampleRunMain() {
	// In package main: keep the main goroutine on the main thread, which
	// macOS needs for the print panel.
	//
	//	func init() { runtime.LockOSThread() }

	goprint.RunMain(func() {
		// The program's work; Dialog may be called from any goroutine.
		_, _, err := goprint.Dialog(context.Background(), goprint.PDFFile("report.pdf"), goprint.DialogOptions{PrintNow: true})
		if err != nil && !errors.Is(err, goprint.ErrCanceled) {
			log.Fatal(err)
		}
	})
}

func ExampleParseMedia() {
	m, err := goprint.ParseMedia("iso_a4_210x297mm")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(m.Width, m.Height)
	// Output: 210000 297000
}
