package ipp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
)

// DefaultDocumentFormat is the document-format sent by [Client.PrintJob]
// unless another one is given.
const DefaultDocumentFormat = "application/pdf"

// Printer is the printer-attributes group of a response, with the most
// common attributes extracted.
type Printer struct {
	// Name is printer-name.
	Name string
	// URI is the first value of printer-uri-supported.
	URI string
	// State is printer-state.
	State PrinterState
	// StateReasons are the printer-state-reasons keywords.
	StateReasons []string
	// Attrs holds all printer attributes of the response.
	Attrs Attributes
}

func newPrinter(attrs Attributes) Printer {
	p := Printer{Attrs: attrs}
	if a, ok := attrs.Get("printer-name"); ok {
		p.Name = a.String()
	}
	if a, ok := attrs.Get("printer-uri-supported"); ok {
		p.URI = a.String()
	}
	if a, ok := attrs.Get("printer-state"); ok {
		n, _ := a.Int()
		p.State = PrinterState(n)
	}
	if a, ok := attrs.Get("printer-state-reasons"); ok {
		p.StateReasons = a.Strings()
	}
	return p
}

// Job is the job-attributes group of a response, with the most common
// attributes extracted.
type Job struct {
	// ID is job-id.
	ID int
	// URI is job-uri.
	URI string
	// State is job-state.
	State JobState
	// StateReasons are the job-state-reasons keywords.
	StateReasons []string
	// StateMessage is job-state-message.
	StateMessage string
	// Attrs holds all job attributes of the response.
	Attrs Attributes

	// Status is the status-code of the response. It is
	// [StatusOKIgnoredOrSubstituted] or [StatusOKConflicting] if some
	// requested attributes were not honored.
	Status Status
	// Unsupported lists the attributes or values the server ignored or
	// substituted; callers can turn them into warnings.
	Unsupported Attributes
}

func newJob(m *Message) *Job {
	j := &Job{Status: m.Status(), Unsupported: m.Unsupported()}
	if g := m.Group(TagJobGroup); g != nil {
		j.Attrs = g.Attrs
	}
	if a, ok := j.Attrs.Get("job-id"); ok {
		j.ID, _ = a.Int()
	}
	if a, ok := j.Attrs.Get("job-uri"); ok {
		j.URI = a.String()
	}
	if a, ok := j.Attrs.Get("job-state"); ok {
		n, _ := a.Int()
		j.State = JobState(n)
	}
	if a, ok := j.Attrs.Get("job-state-reasons"); ok {
		j.StateReasons = a.Strings()
	}
	if a, ok := j.Attrs.Get("job-state-message"); ok {
		j.StateMessage = a.String()
	}
	return j
}

// newRequest builds a request for op with the mandatory operation
// attributes: attributes-charset, attributes-natural-language, printer-uri
// (if printer is not empty), the target attributes and
// requesting-user-name.
func (c *Client) newRequest(op Operation, printer string, target ...Attribute) *Message {
	m := NewRequest(op, 0)
	g := &m.Groups[0].Attrs
	if printer != "" {
		g.Add("printer-uri", URI(c.resolvePrinter(printer)))
	}
	*g = append(*g, target...)
	g.Add("requesting-user-name", Name(c.user))
	return m
}

func addRequested(m *Message, requested []string) {
	if len(requested) == 0 {
		return
	}
	vs := make([]Value, len(requested))
	for i, r := range requested {
		vs[i] = Keyword(r)
	}
	m.operationGroup().Attrs.Add("requested-attributes", vs...)
}

// CUPSGetPrinters lists all printers of a CUPS server (CUPS-Get-Printers).
// requested limits the returned attributes; none means all. A server
// without printers yields an empty list.
func (c *Client) CUPSGetPrinters(ctx context.Context, requested ...string) ([]Printer, error) {
	req := c.newRequest(OpCUPSGetPrinters, "")
	addRequested(req, requested)
	resp, err := c.Do(ctx, req, nil)
	if se := (*StatusError)(nil); errors.As(err, &se) && se.Code == StatusErrorNotFound {
		return nil, nil // CUPS: "No destinations added."
	}
	if err != nil {
		return nil, err
	}
	var ps []Printer
	for _, g := range resp.GroupsByTag(TagPrinterGroup) {
		ps = append(ps, newPrinter(g.Attrs))
	}
	return ps, nil
}

// CUPSGetDefault returns the default printer of a CUPS server
// (CUPS-Get-Default). requested limits the returned attributes; none means
// all. Without a default printer it fails with a *[StatusError] with
// [StatusErrorNotFound].
func (c *Client) CUPSGetDefault(ctx context.Context, requested ...string) (*Printer, error) {
	req := c.newRequest(OpCUPSGetDefault, "")
	addRequested(req, requested)
	resp, err := c.Do(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	return printerResult(resp)
}

// GetPrinterAttributes returns the attributes of printer, a printer URI or
// a CUPS queue name (Get-Printer-Attributes). requested limits the
// returned attributes; none means all.
func (c *Client) GetPrinterAttributes(ctx context.Context, printer string, requested ...string) (*Printer, error) {
	req := c.newRequest(OpGetPrinterAttributes, printer)
	addRequested(req, requested)
	resp, err := c.Do(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	return printerResult(resp)
}

func printerResult(resp *Message) (*Printer, error) {
	g := resp.Group(TagPrinterGroup)
	if g == nil {
		return nil, fmt.Errorf("%w: %s response without printer attributes", ErrMalformed, resp.Status())
	}
	p := newPrinter(g.Attrs)
	return &p, nil
}

// PrintJobOptions are optional parameters of [Client.PrintJob].
type PrintJobOptions struct {
	// JobName is the job-name; empty means none.
	JobName string
	// DocumentFormat is the document MIME type; empty means
	// [DefaultDocumentFormat].
	DocumentFormat string
	// Operation are additional operation attributes, appended after the
	// standard ones.
	Operation Attributes
	// Job are job template attributes such as copies, sides or media-col.
	Job Attributes
}

// PrintJob submits a job with the document read from doc to printer, a
// printer URI or a CUPS queue name (Print-Job). The document is streamed;
// it is not buffered in memory. opts may be nil.
//
// If the server ignored or substituted attributes, the job is returned
// without error and lists them in [Job.Unsupported].
func (c *Client) PrintJob(ctx context.Context, printer string, doc io.Reader, opts *PrintJobOptions) (*Job, error) {
	if doc == nil {
		return nil, invalidf("Print-Job: nil document")
	}
	if opts == nil {
		opts = &PrintJobOptions{}
	}
	req := c.newRequest(OpPrintJob, printer)
	op := &req.Groups[0].Attrs
	if opts.JobName != "" {
		op.Add("job-name", Name(opts.JobName))
	}
	format := opts.DocumentFormat
	if format == "" {
		format = DefaultDocumentFormat
	}
	op.Add("document-format", MimeMediaType(format))
	*op = append(*op, opts.Operation...)
	if len(opts.Job) > 0 {
		req.Groups = append(req.Groups, Group{Tag: TagJobGroup, Attrs: opts.Job})
	}
	resp, err := c.Do(ctx, req, doc)
	if err != nil {
		return nil, err
	}
	return newJob(resp), nil
}

// GetJobAttributes returns the attributes of job jobID on printer, a
// printer URI or a CUPS queue name (Get-Job-Attributes). requested limits
// the returned attributes; none means all.
func (c *Client) GetJobAttributes(ctx context.Context, printer string, jobID int, requested ...string) (*Job, error) {
	id, err := jobIDAttr(jobID)
	if err != nil {
		return nil, err
	}
	req := c.newRequest(OpGetJobAttributes, printer, id)
	addRequested(req, requested)
	resp, err := c.Do(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	return newJob(resp), nil
}

// CancelJob cancels job jobID on printer, a printer URI or a CUPS queue
// name (Cancel-Job).
func (c *Client) CancelJob(ctx context.Context, printer string, jobID int) error {
	id, err := jobIDAttr(jobID)
	if err != nil {
		return err
	}
	_, err = c.Do(ctx, c.newRequest(OpCancelJob, printer, id), nil)
	return err
}

func jobIDAttr(id int) (Attribute, error) {
	if id < 1 || id > math.MaxInt32 {
		return Attribute{}, invalidf("job-id %d out of range", id)
	}
	return Attribute{Name: "job-id", Values: []Value{Integer(id)}}, nil
}
