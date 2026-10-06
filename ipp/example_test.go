package ipp_test

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

func ExampleClient_PrintJob() {
	srv := ipptest.NewServer(ipptest.Printer{Name: "Office", Attrs: ipp.Attributes{
		{Name: "sides-supported", Values: []ipp.Value{ipp.Keyword("one-sided")}},
	}})
	defer srv.Close()

	// For the local CUPS scheduler use ipp.NewCUPSClient().
	c, err := ipp.NewClient(srv.URL)
	if err != nil {
		log.Fatal(err)
	}
	job, err := c.PrintJob(context.Background(), "Office", strings.NewReader("%PDF-1.7 ..."), &ipp.PrintJobOptions{
		JobName: "Invoice 4711",
		Job: ipp.Attributes{
			{Name: "copies", Values: []ipp.Value{ipp.Integer(2)}},
			{Name: "sides", Values: []ipp.Value{ipp.Keyword("two-sided-long-edge")}},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("job", job.ID, job.State)
	for _, a := range job.Unsupported {
		fmt.Println("not honored:", a.Name, a.Strings())
	}
	// Output:
	// job 1 pending
	// not honored: sides [two-sided-long-edge]
}

func ExampleMessage_MarshalBinary() {
	req := ipp.NewRequest(ipp.OpGetPrinterAttributes, 1)
	req.Groups[0].Attrs.Add("printer-uri", ipp.URI("ipp://localhost/printers/Office"))
	req.Groups[0].Attrs.Add("requested-attributes", ipp.Keyword("printer-state"), ipp.Keyword("media-col-default"))
	b, err := req.MarshalBinary()
	if err != nil {
		log.Fatal(err)
	}
	var m ipp.Message
	if err := m.UnmarshalBinary(b); err != nil {
		log.Fatal(err)
	}
	a, _ := m.Group(ipp.TagOperationGroup).Attrs.Get("requested-attributes")
	fmt.Println(m.Operation(), m.Version, a.Strings())
	// Output: Get-Printer-Attributes 2.0 [printer-state media-col-default]
}
