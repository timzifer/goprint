package ipptest

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timzifer/goprint/ipp"
)

// Printer is a print queue served by a [Server].
type Printer struct {
	// Name is the queue name; the printer URI path is /printers/Name.
	Name string
	// Default marks the printer returned by CUPS-Get-Default.
	Default bool
	// Attrs are the printer attributes. printer-name, printer-uri-supported,
	// printer-state and printer-state-reasons are added if missing.
	//
	// For Print-Job, a job template attribute X is reported as unsupported
	// (status successful-ok-ignored-or-substituted-attributes) if Attrs
	// contains X-supported and the requested value is not among its values
	// or outside its rangeOfInteger.
	Attrs ipp.Attributes
}

// Job is a job received by a [Server].
type Job struct {
	ID      int
	Printer string
	// User is the requesting-user-name of the Print-Job request.
	User string
	// Operation and Attrs are the operation and job attributes of the
	// Print-Job request.
	Operation ipp.Attributes
	Attrs     ipp.Attributes
	// Document is the document data.
	Document []byte
	State    ipp.JobState
}

// Server is an in-process IPP server implementing CUPS-Get-Printers,
// CUPS-Get-Default, Get-Printer-Attributes, Print-Job, Get-Job-Attributes
// and Cancel-Job. It is safe for concurrent use.
type Server struct {
	// URL is the base URL of the HTTP server, e.g. "http://127.0.0.1:1234".
	URL string
	// SocketPath is the unix socket the server also listens on, or "" if
	// unix sockets are not supported on this platform.
	SocketPath string

	ts       *httptest.Server
	unix     *http.Server
	unixDir  string
	mu       sync.Mutex
	printers []Printer
	jobs     []Job
	requests []*ipp.Message
	inject   map[ipp.Operation]injected
	latency  time.Duration
	user     string
	pass     string
	auth     bool
}

type injected struct {
	status ipp.Status
	msg    string
}

// NewServer starts a server with the given printers. It listens on a
// loopback TCP port and, where supported, on a unix socket. Call Close when
// done.
func NewServer(printers ...Printer) *Server {
	s := &Server{printers: slices.Clone(printers), inject: map[ipp.Operation]injected{}}
	s.ts = httptest.NewServer(s)
	s.URL = s.ts.URL
	s.listenUnix()
	return s
}

func (s *Server) listenUnix() {
	dir, err := os.MkdirTemp("", "ipptest")
	if err != nil {
		return
	}
	path := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return
	}
	s.unixDir, s.SocketPath = dir, path
	s.unix = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.unix.Serve(l) }() // returns on Close
}

// Close shuts the server down and removes the unix socket.
func (s *Server) Close() {
	if s.unix != nil {
		s.unix.Close()
		os.RemoveAll(s.unixDir)
	}
	s.ts.Close()
}

// AddPrinter adds or replaces (by name) a printer.
func (s *Server) AddPrinter(p Printer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.printers {
		if s.printers[i].Name == p.Name {
			s.printers[i] = p
			return
		}
	}
	s.printers = append(s.printers, p)
}

// Jobs returns the jobs received so far.
func (s *Server) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.jobs)
}

// SetJobState sets the state of job id and reports whether it exists.
func (s *Server) SetJobState(id int, st ipp.JobState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.job(id); j != nil {
		j.State = st
		return true
	}
	return false
}

// Requests returns all decoded requests received so far.
func (s *Server) Requests() []*ipp.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// InjectStatus makes all following requests for op fail with status st and
// status-message msg. A non-error st (e.g. ipp.StatusOK) removes the
// injection.
func (s *Server) InjectStatus(op ipp.Operation, st ipp.Status, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.IsError() {
		s.inject[op] = injected{st, msg}
	} else {
		delete(s.inject, op)
	}
}

// SetLatency delays every response by d.
func (s *Server) SetLatency(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latency = d
}

// RequireAuth makes the server answer HTTP 401 unless a request carries
// HTTP Basic credentials user and pass.
func (s *Server) RequireAuth(user, pass string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user, s.pass, s.auth = user, pass, true
}

// ServeHTTP implements [http.Handler].
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	latency, auth, user, pass := s.latency, s.auth, s.user, s.pass
	s.mu.Unlock()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != ipp.ContentType {
		http.Error(w, "unsupported content type "+ct, http.StatusUnsupportedMediaType)
		return
	}
	if auth {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="CUPS"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	// Read the whole body first: only then does net/http notice a client
	// that goes away and cancel r.Context() during the latency wait.
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	if latency > 0 {
		t := time.NewTimer(latency)
		select {
		case <-t.C:
		case <-r.Context().Done():
			t.Stop()
			return
		}
	}
	body := bytes.NewReader(data)
	req, err := ipp.Decode(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	resp := s.handle(r, req, body)
	resp.RequestID = req.RequestID
	b, err := resp.MarshalBinary()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ipp.ContentType)
	_, _ = w.Write(b) // the client may have gone away
}

func errorResponse(st ipp.Status, msg string) *ipp.Message {
	m := ipp.NewResponse(st, 0)
	if msg != "" {
		m.Groups[0].Attrs.Add("status-message", ipp.Text(msg))
	}
	return m
}

func (s *Server) handle(r *http.Request, req *ipp.Message, body io.Reader) *ipp.Message {
	if v := req.Version.Major(); v < 1 || v > 2 {
		return errorResponse(ipp.StatusErrorVersionNotSupported, "")
	}
	op := req.Operation()
	var opAttrs ipp.Attributes
	if len(req.Groups) > 0 && req.Groups[0].Tag == ipp.TagOperationGroup {
		opAttrs = req.Groups[0].Attrs
	}
	if len(opAttrs) < 2 || opAttrs[0].Name != "attributes-charset" ||
		opAttrs[1].Name != "attributes-natural-language" {
		return errorResponse(ipp.StatusErrorBadRequest, "missing attributes-charset or attributes-natural-language")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if inj, ok := s.inject[op]; ok {
		return errorResponse(inj.status, inj.msg)
	}
	host := r.Host
	switch op {
	case ipp.OpCUPSGetPrinters:
		if len(s.printers) == 0 {
			return errorResponse(ipp.StatusErrorNotFound, "No destinations added.")
		}
		resp := ipp.NewResponse(ipp.StatusOK, 0)
		for _, p := range s.printers {
			resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagPrinterGroup, Attrs: printerAttrs(p, host, opAttrs)})
		}
		return resp
	case ipp.OpCUPSGetDefault:
		for _, p := range s.printers {
			if p.Default {
				return printerResponse(p, host, opAttrs)
			}
		}
		return errorResponse(ipp.StatusErrorNotFound, "No default printer.")
	}
	p, st := s.target(opAttrs)
	if st != ipp.StatusOK {
		return errorResponse(st, "printer not found")
	}
	switch op {
	case ipp.OpGetPrinterAttributes:
		return printerResponse(*p, host, opAttrs)
	case ipp.OpPrintJob:
		return s.printJob(p, host, req, body)
	case ipp.OpGetJobAttributes, ipp.OpCancelJob:
		a, _ := opAttrs.Get("job-id")
		id, _ := a.Int()
		j := s.job(id)
		if j == nil || j.Printer != p.Name {
			return errorResponse(ipp.StatusErrorNotFound, "job not found")
		}
		if op == ipp.OpGetJobAttributes {
			resp := ipp.NewResponse(ipp.StatusOK, 0)
			resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagJobGroup, Attrs: filter(jobAttrs(j, host), opAttrs)})
			return resp
		}
		if j.State.Terminal() {
			return errorResponse(ipp.StatusErrorNotPossible, "job already "+j.State.String())
		}
		j.State = ipp.JobCanceled
		return ipp.NewResponse(ipp.StatusOK, 0)
	}
	return errorResponse(ipp.StatusErrorOperationNotSupported, "")
}

// target returns the printer named by the printer-uri operation attribute.
func (s *Server) target(opAttrs ipp.Attributes) (*Printer, ipp.Status) {
	a, ok := opAttrs.Get("printer-uri")
	if !ok {
		return nil, ipp.StatusErrorBadRequest
	}
	u, err := url.Parse(a.String())
	if err != nil {
		return nil, ipp.StatusErrorBadRequest
	}
	name, ok := strings.CutPrefix(u.Path, "/printers/")
	if !ok {
		return nil, ipp.StatusErrorNotFound
	}
	for i := range s.printers {
		if s.printers[i].Name == name {
			return &s.printers[i], ipp.StatusOK
		}
	}
	return nil, ipp.StatusErrorNotFound
}

func (s *Server) job(id int) *Job {
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			return &s.jobs[i]
		}
	}
	return nil
}

func (s *Server) printJob(p *Printer, host string, req *ipp.Message, body io.Reader) *ipp.Message {
	doc, err := io.ReadAll(body)
	if err != nil {
		return errorResponse(ipp.StatusErrorDocumentAccess, err.Error())
	}
	op := req.Groups[0].Attrs
	j := Job{ID: len(s.jobs) + 1, Printer: p.Name, Operation: op, Document: doc, State: ipp.JobPending}
	if a, ok := op.Get("requesting-user-name"); ok {
		j.User = a.String()
	}
	if g := req.Group(ipp.TagJobGroup); g != nil {
		j.Attrs = g.Attrs
	}
	var unsupported ipp.Attributes
	for _, a := range j.Attrs {
		if !supported(p.Attrs, a) {
			unsupported = append(unsupported, a)
		}
	}
	s.jobs = append(s.jobs, j)
	st := ipp.StatusOK
	if len(unsupported) > 0 {
		st = ipp.StatusOKIgnoredOrSubstituted
	}
	resp := ipp.NewResponse(st, 0)
	if len(unsupported) > 0 {
		resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagUnsupportedGroup, Attrs: unsupported})
	}
	resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagJobGroup, Attrs: jobAttrs(&j, host)})
	return resp
}

// supported checks a job template attribute against X-supported.
func supported(printer ipp.Attributes, a ipp.Attribute) bool {
	sup, ok := printer.Get(a.Name + "-supported")
	if !ok {
		return true
	}
	for _, v := range a.Values {
		if _, ok := v.(ipp.Collection); ok {
			continue
		}
		if !slices.ContainsFunc(sup.Values, func(s ipp.Value) bool { return matches(s, v) }) {
			return false
		}
	}
	return true
}

func matches(sup, v ipp.Value) bool {
	if r, ok := sup.(ipp.Range); ok {
		n, ok := v.(ipp.Integer)
		return ok && int32(n) >= r.Lower && int32(n) <= r.Upper
	}
	return sup.String() == v.String()
}

func printerResponse(p Printer, host string, opAttrs ipp.Attributes) *ipp.Message {
	resp := ipp.NewResponse(ipp.StatusOK, 0)
	resp.Groups = append(resp.Groups, ipp.Group{Tag: ipp.TagPrinterGroup, Attrs: printerAttrs(p, host, opAttrs)})
	return resp
}

func printerAttrs(p Printer, host string, opAttrs ipp.Attributes) ipp.Attributes {
	as := slices.Clone(p.Attrs)
	setDefault := func(name string, v ipp.Value) {
		if _, ok := as.Get(name); !ok {
			as.Add(name, v)
		}
	}
	setDefault("printer-name", ipp.Name(p.Name))
	setDefault("printer-uri-supported", ipp.URI("ipp://"+host+"/printers/"+url.PathEscape(p.Name)))
	setDefault("printer-state", ipp.Enum(ipp.PrinterIdle))
	setDefault("printer-state-reasons", ipp.Keyword("none"))
	return filter(as, opAttrs)
}

func jobAttrs(j *Job, host string) ipp.Attributes {
	reason := map[ipp.JobState]string{
		ipp.JobCanceled:  "job-canceled-by-user",
		ipp.JobAborted:   "aborted-by-system",
		ipp.JobCompleted: "job-completed-successfully",
	}[j.State]
	if reason == "" {
		reason = "none"
	}
	return ipp.Attributes{
		{Name: "job-id", Values: []ipp.Value{ipp.Integer(j.ID)}},
		{Name: "job-uri", Values: []ipp.Value{ipp.URI("ipp://" + host + "/jobs/" + strconv.Itoa(j.ID))}},
		{Name: "job-state", Values: []ipp.Value{ipp.Enum(j.State)}},
		{Name: "job-state-reasons", Values: []ipp.Value{ipp.Keyword(reason)}},
		{Name: "job-printer-uri", Values: []ipp.Value{ipp.URI("ipp://" + host + "/printers/" + url.PathEscape(j.Printer))}},
	}
}

// filter applies the requested-attributes operation attribute.
func filter(as ipp.Attributes, opAttrs ipp.Attributes) ipp.Attributes {
	req, ok := opAttrs.Get("requested-attributes")
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
