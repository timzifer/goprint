package virtualprinter

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/dnssd"
	"github.com/timzifer/goprint/ipp"
)

// Server serves the printers of a [Provider] over IPP, so that other
// processes and devices can print to them: goprint's own IPP code
// (printer URIs, [goprint.IPPEverywhere]), CUPS, or a phone. A printer is
// at /printers/<name>. Jobs land in the provider as if printed with
// [Provider.Print]: warnings, Strict (ipp-attribute-fidelity), offline
// printers, FailNext, HoldJobs and Latency apply alike.
//
// The server speaks plain IPP over HTTP and accepts PDF documents.
type Server struct {
	p *Provider

	mu      sync.Mutex
	created map[int]createdJob // Create-Job without Send-Document yet

	ln        net.Listener
	http      *http.Server
	responder *dnssd.Responder
}

type createdJob struct {
	printer string
	title   string
	s       goprint.Settings
	attrs   ipp.Attributes // job template attributes of the request
}

// Handler returns the IPP handler for p, to mount in an own HTTP server.
// It serves the paths /printers/<name>.
func (p *Provider) Handler() http.Handler { return newServer(p) }

func newServer(p *Provider) *Server {
	return &Server{p: p, created: map[int]createdJob{}}
}

// Listen serves p over IPP on addr (e.g. ":631" or "127.0.0.1:0") until
// Close.
func (p *Provider) Listen(addr string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := newServer(p)
	s.ln = ln
	s.http = &http.Server{Handler: s}
	go func() { _ = s.http.Serve(ln) }() // returns on Close
	return s, nil
}

// Addr returns the address the server listens on.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// PrinterURI returns the URI of the named printer, with the listening
// address as host (an unspecified address becomes localhost).
func (s *Server) PrinterURI(name string) string {
	host, port, _ := net.SplitHostPort(s.ln.Addr().String())
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "localhost"
	}
	return "ipp://" + net.JoinHostPort(host, port) + printerPath(name)
}

// Close stops the server and its announcement.
func (s *Server) Close() error {
	s.mu.Lock()
	r := s.responder
	s.responder = nil
	s.mu.Unlock()
	if r != nil {
		_ = r.Close() // sends goodbye records
	}
	return s.http.Close()
}

// Advertise announces the provider's printers on the local network with
// DNS-SD (multicast DNS), as IPP Everywhere printers (_ipp._tcp, subtypes
// _print and _universal), until Close. goprint's IPPEverywhere provider,
// CUPS and phones find them then. Printers added or removed later are
// answered for as they are; the TXT records carry the IPP Everywhere and
// AirPrint keys for a PDF printer. The responder shares port 5353 with
// the system's (Avahi, Bonjour, Windows); it does not probe for name
// conflicts, so printer names should be unique on the network.
func (s *Server) Advertise() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.responder != nil {
		return nil
	}
	_, portStr, _ := net.SplitHostPort(s.ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	host := hostLabel() + "-goprint-" + portStr
	r, err := dnssd.Respond(host, func() []dnssd.Instance { return s.instances(host, port) })
	if err != nil {
		return fmt.Errorf("virtualprinter: announcing printers: %w", err)
	}
	s.responder = r
	return nil
}

// instances are the DNS-SD instances of the provider's printers.
func (s *Server) instances(host string, port int) []dnssd.Instance {
	ps, _ := s.p.Printers(context.Background())
	out := make([]dnssd.Instance, 0, len(ps))
	for _, pr := range ps {
		yn := map[bool]string{true: "T", false: "F"}
		ty := pr.Description
		if ty == "" {
			ty = pr.Name
		}
		sum := sha1.Sum([]byte(host + "/" + pr.Name))
		uuid := fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
		out = append(out, dnssd.Instance{
			Name:     pr.Name,
			Type:     "_ipp._tcp",
			Subtypes: []string{"_print", "_universal"},
			Port:     port,
			TXT: []string{
				"txtvers=1",
				"qtotal=1",
				"rp=" + strings.TrimPrefix(printerPath(pr.Name), "/"),
				"ty=" + ty,
				"note=" + pr.Location,
				"product=(goprint virtual printer)",
				"pdl=application/pdf",
				"Color=" + yn[pr.Caps.Color],
				"Duplex=" + yn[pr.Caps.Duplex],
				"UUID=" + uuid,
				"kind=document",
			},
		})
	}
	return out
}

// hostLabel is the machine's host name as part of a DNS label: at most
// 40 characters, so that "-goprint-<port>" fits the 63 of a label. Longer
// names (CI machines have them) are cut and keep a hash of the rest.
func hostLabel() string {
	h, _ := os.Hostname()
	return labelOf(h)
}

func labelOf(h string) string {
	h, _, _ = strings.Cut(h, ".")
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	l := b.String()
	if l == "" {
		return "host"
	}
	if len(l) > 40 {
		sum := sha1.Sum([]byte(l))
		l = fmt.Sprintf("%s-%x", strings.TrimRight(l[:31], "-"), sum[:4])
	}
	return l
}

func printerPath(name string) string { return "/printers/" + url.PathEscape(name) }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "IPP requests are POSTed", http.StatusMethodNotAllowed)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != ipp.ContentType {
		http.Error(w, "unsupported content type "+ct, http.StatusUnsupportedMediaType)
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	body := bytes.NewReader(data)
	req, err := ipp.Decode(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := s.handle(r.Context(), r.Host, req, body)
	resp.RequestID = req.RequestID
	b, err := resp.MarshalBinary()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ipp.ContentType)
	_, _ = w.Write(b)
}

func errorResponse(st ipp.Status, format string, args ...any) *ipp.Message {
	m := ipp.NewResponse(st, 0)
	m.Groups[0].Attrs.Add("status-message", ipp.Text(fmt.Sprintf(format, args...)))
	return m
}

func (s *Server) handle(ctx context.Context, host string, req *ipp.Message, body io.Reader) *ipp.Message {
	if v := req.Version.Major(); v < 1 || v > 2 {
		return errorResponse(ipp.StatusErrorVersionNotSupported, "IPP %s is not supported", req.Version)
	}
	var op ipp.Attributes
	if len(req.Groups) > 0 && req.Groups[0].Tag == ipp.TagOperationGroup {
		op = req.Groups[0].Attrs
	}
	if len(op) < 2 || op[0].Name != "attributes-charset" || op[1].Name != "attributes-natural-language" {
		return errorResponse(ipp.StatusErrorBadRequest, "missing attributes-charset or attributes-natural-language")
	}
	name, ok := targetPrinter(op)
	if !ok {
		return errorResponse(ipp.StatusErrorBadRequest, "missing or invalid printer-uri")
	}
	pr, offline, ok := s.p.printer(name)
	if !ok {
		return errorResponse(ipp.StatusErrorNotFound, "no virtual printer %q", name)
	}
	switch req.Operation() {
	case ipp.OpGetPrinterAttributes:
		resp := ipp.NewResponse(ipp.StatusOK, 0)
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagPrinterGroup,
			Attrs: filter(printerAttributes(pr, offline, host), op)})
		return resp
	case ipp.OpValidateJob:
		_, unsupported, err := s.settings(pr, req)
		if err != nil {
			return errorResponse(ipp.StatusErrorAttributesOrValues, "%v", err)
		}
		return okResponse(unsupported)
	case ipp.OpPrintJob:
		return s.printJob(ctx, pr, host, req, body, 0)
	case ipp.OpCreateJob:
		set, unsupported, err := s.settings(pr, req)
		if err != nil {
			return errorResponse(ipp.StatusErrorAttributesOrValues, "%v", err)
		}
		id := s.p.reserveID()
		s.mu.Lock()
		s.created[id] = createdJob{printer: pr.Name, title: jobName(op), s: set, attrs: jobGroup(req)}
		s.mu.Unlock()
		resp := okResponse(unsupported)
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagJobGroup,
			Attrs: jobAttributes(id, pr.Name, goprint.JobPending, host)})
		return resp
	case ipp.OpSendDocument:
		id := jobID(op)
		s.mu.Lock()
		c, ok := s.created[id]
		delete(s.created, id)
		s.mu.Unlock()
		if !ok || c.printer != pr.Name {
			return errorResponse(ipp.StatusErrorNotFound, "no job %d waiting for its document", id)
		}
		return s.print(ctx, pr, host, op, c.attrs, body, c.s, c.title, nil, id)
	case ipp.OpGetJobAttributes:
		id := jobID(op)
		st, ok := s.jobState(pr.Name, id)
		if !ok {
			return errorResponse(ipp.StatusErrorNotFound, "no job %d", id)
		}
		resp := ipp.NewResponse(ipp.StatusOK, 0)
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagJobGroup,
			Attrs: filter(jobAttributes(id, pr.Name, st, host), op)})
		return resp
	case ipp.OpGetJobs:
		resp := ipp.NewResponse(ipp.StatusOK, 0)
		for _, j := range s.p.Jobs() {
			if j.Printer == pr.Name {
				resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagJobGroup,
					Attrs: filter(jobAttributes(j.ID, j.Printer, j.State(), host), op)})
			}
		}
		return resp
	case ipp.OpCancelJob:
		id := jobID(op)
		s.mu.Lock()
		_, created := s.created[id]
		delete(s.created, id)
		s.mu.Unlock()
		if created {
			return ipp.NewResponse(ipp.StatusOK, 0)
		}
		j := s.p.job(id)
		if j == nil || j.Printer != pr.Name {
			return errorResponse(ipp.StatusErrorNotFound, "no job %d", id)
		}
		if err := j.SetState(goprint.JobCanceled); err != nil {
			return errorResponse(ipp.StatusErrorNotPossible, "%v", err)
		}
		return ipp.NewResponse(ipp.StatusOK, 0)
	}
	return errorResponse(ipp.StatusErrorOperationNotSupported, "operation %s is not supported", req.Operation())
}

func (s *Server) printJob(ctx context.Context, pr Printer, host string, req *ipp.Message, body io.Reader, id int) *ipp.Message {
	set, unsupported, err := s.settings(pr, req)
	if err != nil {
		return errorResponse(ipp.StatusErrorAttributesOrValues, "%v", err)
	}
	op := req.Groups[0].Attrs
	return s.print(ctx, pr, host, op, jobGroup(req), body, set, jobName(op), unsupported, id)
}

// jobGroup returns the job template attributes of a request.
func jobGroup(req *ipp.Message) ipp.Attributes {
	if g := req.Group(ipp.TagJobGroup); g != nil {
		return g.Attrs
	}
	return nil
}

// print reads the document and prints it through the provider.
func (s *Server) print(ctx context.Context, pr Printer, host string, op, attrs ipp.Attributes, body io.Reader,
	set goprint.Settings, title string, unsupported ipp.Attributes, id int) *ipp.Message {
	if a, ok := op.Get("document-format"); ok {
		if f := a.String(); f != "application/pdf" && f != "application/octet-stream" {
			return errorResponse(ipp.StatusErrorDocumentFormat, "document-format %q is not supported; send PDF", f)
		}
	}
	if a, ok := op.Get("compression"); ok && a.String() != "none" {
		return errorResponse(ipp.StatusErrorCompression, "compression %q is not supported", a.String())
	}
	doc, err := io.ReadAll(body)
	if err != nil {
		return errorResponse(ipp.StatusErrorDocumentAccess, "%v", err)
	}
	if !bytes.HasPrefix(doc, []byte("%PDF")) {
		return errorResponse(ipp.StatusErrorDocumentFormatError, "the document is not a PDF")
	}
	set.Printer = pr.Name
	j, err := s.p.print(ctx, goprint.PDFBytes(title, doc), set, id)
	switch {
	case errors.Is(err, goprint.ErrBusy):
		return errorResponse(ipp.StatusErrorNotAcceptingJobs, "%v", err)
	case errors.Is(err, goprint.ErrUnsupported):
		return errorResponse(ipp.StatusErrorAttributesOrValues, "%v", err)
	case err != nil:
		return errorResponse(ipp.StatusErrorInternal, "%v", err)
	}
	// The printer could not honor these: report them as the client sent them.
	for _, w := range j.Warnings {
		for _, name := range attributesFor(w.Setting) {
			if a, ok := attrs.Get(name); ok {
				unsupported = appendOnce(unsupported, a)
			}
		}
	}
	resp := okResponse(unsupported)
	resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagJobGroup,
		Attrs: jobAttributes(j.ID, j.Printer, j.State(), host)})
	return resp
}

// jobState returns the state of a job, also of one created and waiting
// for its document.
func (s *Server) jobState(printer string, id int) (goprint.JobState, bool) {
	s.mu.Lock()
	c, created := s.created[id]
	s.mu.Unlock()
	if created {
		return goprint.JobPending, c.printer == printer
	}
	j := s.p.job(id)
	if j == nil || j.Printer != printer {
		return 0, false
	}
	return j.State(), true
}

// settings returns the settings of a job request, and the attributes it
// does not take. ipp-attribute-fidelity sets Strict.
func (s *Server) settings(pr Printer, req *ipp.Message) (goprint.Settings, ipp.Attributes, error) {
	op := req.Groups[0].Attrs
	set, unsupported := settingsFromIPP(jobGroup(req))
	if a, ok := op.Get("ipp-attribute-fidelity"); ok {
		set.Strict, _ = a.Bool()
	}
	if set.Strict && len(unsupported) > 0 {
		return set, unsupported, fmt.Errorf("%s is not supported", unsupported[0].Name)
	}
	if set.Strict {
		if w := check(set, pr.Caps); len(w) > 0 {
			return set, unsupported, fmt.Errorf("%s", w[0])
		}
	}
	return set, unsupported, nil
}

func okResponse(unsupported ipp.Attributes) *ipp.Message {
	if len(unsupported) == 0 {
		return ipp.NewResponse(ipp.StatusOK, 0)
	}
	resp := ipp.NewResponse(ipp.StatusOKIgnoredOrSubstituted, 0)
	resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagUnsupportedGroup, Attrs: unsupported})
	return resp
}

// targetPrinter returns the printer name of the printer-uri.
func targetPrinter(op ipp.Attributes) (string, bool) {
	a, ok := op.Get("printer-uri")
	if !ok {
		return "", false
	}
	u, err := url.Parse(a.String())
	if err != nil {
		return "", false
	}
	return strings.CutPrefix(u.Path, "/printers/")
}

func jobID(op ipp.Attributes) int {
	a, _ := op.Get("job-id")
	id, _ := a.Int()
	return id
}

func jobName(op ipp.Attributes) string {
	if a, ok := op.Get("job-name"); ok {
		return a.String()
	}
	if a, ok := op.Get("document-name"); ok {
		return a.String()
	}
	return ""
}

func appendOnce(as ipp.Attributes, a ipp.Attribute) ipp.Attributes {
	if slices.ContainsFunc(as, func(x ipp.Attribute) bool { return x.Name == a.Name }) {
		return as
	}
	return append(as, a)
}

// attributesFor names the IPP attributes that carry a setting.
func attributesFor(setting string) []string {
	switch setting {
	case "Media", "Tray":
		return []string{"media", "media-col"}
	case "Duplex":
		return []string{"sides"}
	case "Color":
		return []string{"print-color-mode"}
	case "Quality":
		return []string{"print-quality"}
	}
	return nil
}

var jobStates = map[goprint.JobState]ipp.JobState{
	goprint.JobPending:    ipp.JobPending,
	goprint.JobProcessing: ipp.JobProcessing,
	goprint.JobCompleted:  ipp.JobCompleted,
	goprint.JobCanceled:   ipp.JobCanceled,
	goprint.JobAborted:    ipp.JobAborted,
}

func jobAttributes(id int, printer string, st goprint.JobState, host string) ipp.Attributes {
	reason := map[goprint.JobState]string{
		goprint.JobCanceled:  "job-canceled-by-user",
		goprint.JobAborted:   "aborted-by-system",
		goprint.JobCompleted: "job-completed-successfully",
	}[st]
	if reason == "" {
		reason = "none"
	}
	return ipp.Attributes{
		{Name: "job-id", Values: []ipp.Value{ipp.Integer(id)}},
		{Name: "job-uri", Values: []ipp.Value{ipp.URI("ipp://" + host + "/jobs/" + strconv.Itoa(id))}},
		{Name: "job-state", Values: []ipp.Value{ipp.Enum(jobStates[st])}},
		{Name: "job-state-reasons", Values: []ipp.Value{ipp.Keyword(reason)}},
		{Name: "job-printer-uri", Values: []ipp.Value{ipp.URI("ipp://" + host + printerPath(printer))}},
	}
}

// filter applies the requested-attributes operation attribute.
func filter(as ipp.Attributes, op ipp.Attributes) ipp.Attributes {
	req, ok := op.Get("requested-attributes")
	if !ok {
		return as
	}
	names := req.Strings()
	for _, all := range []string{"all", "printer-description", "job-template", "job-description"} {
		if slices.Contains(names, all) {
			return as
		}
	}
	var out ipp.Attributes
	for _, a := range as {
		if slices.Contains(names, a.Name) {
			out = append(out, a)
		}
	}
	return out
}
