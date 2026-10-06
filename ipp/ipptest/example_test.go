package ipptest_test

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

func ExampleServer() {
	srv := ipptest.NewServer(ipptest.Printer{Name: "Office", Default: true})
	defer srv.Close()
	srv.InjectStatus(ipp.OpGetPrinterAttributes, ipp.StatusErrorBusy, "try later")

	c, err := ipp.NewClient(srv.URL)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.PrintJob(ctx, "Office", strings.NewReader("%PDF-1.7"), nil); err != nil {
		log.Fatal(err)
	}
	_, err = c.GetPrinterAttributes(ctx, "Office")
	fmt.Println(err)

	for _, j := range srv.Jobs() {
		format, _ := j.Operation.Get("document-format")
		fmt.Printf("job %d on %s: %q (%s)\n", j.ID, j.Printer, j.Document, format.String())
	}
	// Output:
	// ipp: server-error-busy: try later
	// job 1 on Office: "%PDF-1.7" (application/pdf)
}
