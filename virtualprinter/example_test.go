package virtualprinter_test

import (
	"context"
	"fmt"
	"log"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/virtualprinter"
)

func Example() {
	ctx := context.Background()
	vp := virtualprinter.New("virtual", virtualprinter.Office("Office"), virtualprinter.Label("Label-62"))
	// Programs add vp next to goprint.System(); this example uses it alone.
	c := goprint.NewClient(vp)

	job, err := c.Print(ctx, goprint.PDFBytes("Shipping label", []byte("%PDF-1.7 ...")), goprint.Settings{
		Provider: "virtual",
		Printer:  "Label-62",
		Duplex:   goprint.DuplexLongEdge,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, w := range job.Warnings() {
		fmt.Println("warning:", w)
	}
	j := vp.Jobs()[0]
	fmt.Printf("%s on %s: %d bytes, %s\n", j.Title, j.Printer, len(j.PDF), j.State())
	// Output:
	// warning: Duplex: printer cannot print two-sided
	// Shipping label on Label-62: 12 bytes, completed
}

func ExampleProvider_HoldJobs() {
	ctx := context.Background()
	vp := virtualprinter.New("virtual", virtualprinter.Office("Office"))
	vp.HoldJobs(true)

	job, err := goprint.NewClient(vp).Print(ctx, goprint.PDFBytes("Report", []byte("%PDF-1.7")), goprint.Settings{Provider: "virtual"})
	if err != nil {
		log.Fatal(err)
	}
	st, _ := job.State(ctx)
	fmt.Println(st)

	// The test drives the job like a printer would.
	j := vp.Jobs()[0]
	_ = j.SetState(goprint.JobProcessing)
	_ = j.SetState(goprint.JobCompleted)
	fmt.Println(job.Wait(ctx))
	// Output:
	// pending
	// <nil>
}
