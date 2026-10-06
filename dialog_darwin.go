//go:build darwin

package goprint

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/timzifer/goprint/internal/macprint"
	"github.com/timzifer/goprint/ipp"
)

// darwinBackend prints headless through CUPS (ippBackend) and shows the
// AppKit print panel with the PDFKit preview for Dialog.
type darwinBackend struct {
	ippBackend
}

func (b darwinBackend) printers(ctx context.Context) ([]Printer, error) {
	ps, err := b.ippBackend.printers(ctx)
	for i := range ps {
		ps[i].Caps.DialogPreview = true
	}
	return ps, err
}

func (b darwinBackend) capabilities(ctx context.Context, printer string) (Capabilities, error) {
	c, err := b.ippBackend.capabilities(ctx, printer)
	if err == nil {
		c.DialogPreview = true
	}
	return c, err
}

// readPDF returns the whole document as PDF; PDFKit takes it as NSData.
func readPDF(doc Document) ([]byte, error) {
	src, err := doc.open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	b, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("goprint: reading document: %w", err)
	}
	return b, nil
}

func docTitle(doc Document) string {
	if doc.Title == "" {
		return "Document"
	}
	return doc.Title
}

// dialog shows the print panel. The panel runs on the main thread (see
// RunMain); ctx is checked before it opens but cannot close it.
func (b darwinBackend) dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	if err := ctx.Err(); err != nil {
		return nil, Settings{}, err
	}
	pdf, err := readPDF(doc)
	if err != nil {
		return nil, Settings{}, err
	}
	title := docTitle(doc)
	o, warnings := toMacOptions(opts.Settings, title)
	if opts.RequirePrinter && o.Printer == "" && opts.Settings.Printer != "" {
		return nil, Settings{}, fmt.Errorf("%w: the macOS print panel cannot preselect %q (RequirePrinter)", ErrUnsupported, opts.Settings.Printer)
	}
	if opts.Settings.Strict && len(warnings) > 0 {
		return nil, Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}

	baseline := -1
	if opts.PrintNow {
		baseline = b.newestJobID(ctx)
	}
	res, err := macprint.Dialog(pdf, o, opts.PrintNow)
	if err != nil {
		return nil, Settings{}, err
	}
	chosen := fromMacResult(res, opts.Settings)
	if !opts.PrintNow {
		return nil, chosen, nil
	}
	return b.dialogJob(ctx, res, chosen.Printer, title, baseline, warnings), chosen, nil
}

// macPrintToFile builds and runs the print operation exactly as the dialog
// does, but without panel and with the output saved as PDF to path. Tests
// use it to exercise the settings mapping and PDFKit printing for real.
func macPrintToFile(doc Document, s Settings, path string) (Settings, []Warning, error) {
	pdf, err := readPDF(doc)
	if err != nil {
		return Settings{}, nil, err
	}
	o, warnings := toMacOptions(s, docTitle(doc))
	res, err := macprint.PrintToFile(pdf, o, path)
	if err != nil {
		return Settings{}, warnings, err
	}
	return fromMacResult(res, s), warnings, nil
}

// dialogJob returns the job the print operation submitted. AppKit hands
// the document to the macOS print system without telling the job id, so
// it is looked up in CUPS (Get-Jobs, newest job of this user above the
// baseline taken before the dialog). If the user saved a PDF or opened
// Preview, or the job cannot be found, the returned Job cannot be tracked
// and reports completed.
func (b darwinBackend) dialogJob(ctx context.Context, res macprint.Result, printer, title string, baseline int, warnings []Warning) *Job {
	if res.Disposition != "" && res.Disposition != macprint.DispositionSpool {
		return &Job{b: handedOffJob{}, warnings: warnings}
	}
	if baseline >= 0 {
		jobs, err := b.listJobs(ctx)
		if j, ok := pickSpooledJob(jobs, baseline, title); err == nil && ok {
			if j.Printer == "" {
				j.Printer = printer
			}
			if c, err := b.newClient(); err == nil {
				return &Job{b: &ippJob{c: c, printer: j.Printer, jobID: j.ID}, warnings: warnings}
			}
		}
	}
	warnings = append(warnings, Warning{"Job", "the job was handed to the macOS print system and cannot be tracked"})
	return &Job{b: handedOffJob{}, warnings: warnings}
}

// newestJobID returns the highest job id of the current user in CUPS, 0 if
// there is none, or -1 if CUPS cannot be asked.
func (b darwinBackend) newestJobID(ctx context.Context) int {
	jobs, err := b.listJobs(ctx)
	if err != nil {
		return -1
	}
	id := 0
	for _, j := range jobs {
		id = max(id, j.ID)
	}
	return id
}

// listJobs lists all jobs (including completed ones) of the current user
// on all CUPS queues.
func (b darwinBackend) listJobs(ctx context.Context) ([]spooledJob, error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	c, err := b.newClient()
	if err != nil {
		return nil, err
	}
	// The server root URI addresses all queues.
	root := strings.TrimSuffix(c.PrinterURI(""), "printers/")
	req := ipp.NewRequest(ipp.OpGetJobs, 0)
	g := &req.Groups[0].Attrs
	g.Add("printer-uri", ipp.URI(root))
	g.Add("requesting-user-name", ipp.Name(c.UserName()))
	g.Add("which-jobs", ipp.Keyword("all"))
	g.Add("my-jobs", ipp.Boolean(true))
	g.Add("requested-attributes", ipp.Keyword("job-id"), ipp.Keyword("job-name"), ipp.Keyword("job-printer-uri"))
	resp, err := c.Do(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	var jobs []spooledJob
	for _, grp := range resp.GroupsByTag(ipp.TagJobGroup) {
		var j spooledJob
		if a, ok := grp.Attrs.Get("job-id"); ok {
			j.ID, _ = a.Int()
		}
		if a, ok := grp.Attrs.Get("job-name"); ok {
			j.Name = a.String()
		}
		if a, ok := grp.Attrs.Get("job-printer-uri"); ok {
			if u, err := url.Parse(a.String()); err == nil && strings.HasPrefix(u.Path, "/printers/") {
				j.Printer = path.Base(u.Path)
			}
		}
		if j.ID > 0 {
			jobs = append(jobs, j)
		}
	}
	return jobs, nil
}

// handedOffJob is a job the macOS print system took over without a
// trackable id (or a PDF the user saved from the panel).
type handedOffJob struct{}

func (handedOffJob) id() string                              { return "" }
func (handedOffJob) state(context.Context) (JobState, error) { return JobCompleted, nil }
func (handedOffJob) wait(context.Context) error              { return nil }
func (handedOffJob) cancel(context.Context) error {
	return fmt.Errorf("%w: the job cannot be tracked on macOS", ErrUnsupported)
}
