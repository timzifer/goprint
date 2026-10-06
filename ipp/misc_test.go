package ipp

import (
	"errors"
	"io/fs"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestMessageHelpers(t *testing.T) {
	m := NewRequest(OpGetJobs, 9)
	if m.Operation() != OpGetJobs || m.RequestID != 9 || m.Version != Version20 {
		t.Fatalf("NewRequest = %+v", m)
	}
	if m.Group(TagJobGroup) != nil {
		t.Error("Group(job) != nil")
	}
	g := m.AddGroup(TagJobGroup)
	g.Attrs.Add("job-id", Integer(1))
	m.AddGroup(TagJobGroup).Attrs.Add("job-id", Enum(2))
	if gs := m.GroupsByTag(TagJobGroup); len(gs) != 2 {
		t.Errorf("GroupsByTag = %d groups", len(gs))
	}
	if m.operationGroup() != &m.Groups[0] {
		t.Error("operationGroup is not the first group")
	}
	m2 := &Message{}
	m2.operationGroup().Attrs.Add("x", Integer(1))
	if len(m2.Groups) != 1 || m2.Groups[0].Tag != TagOperationGroup {
		t.Errorf("operationGroup on empty message: %+v", m2)
	}
	if m.StatusMessage() != "" || m2.StatusMessage() != "" || (&Message{}).StatusMessage() != "" {
		t.Error("unexpected status message")
	}
	r := NewResponse(StatusErrorNotFound, 9)
	r.Groups[0].Attrs.Add("status-message", Text("gone"))
	if r.Status() != StatusErrorNotFound || r.StatusMessage() != "gone" {
		t.Errorf("response = %v %q", r.Status(), r.StatusMessage())
	}
	r.AddGroup(TagUnsupportedGroup).Attrs.Add("a", Unsupported)
	r.AddGroup(TagUnsupportedGroup).Attrs.Add("b", Keyword("x"))
	if u := r.Unsupported(); len(u) != 2 || u[0].Name != "a" || u[1].Name != "b" {
		t.Errorf("Unsupported = %+v", u)
	}
}

func TestAttributes(t *testing.T) {
	var as Attributes
	as.Add("a", Integer(1))
	as.Set("b", Keyword("x"), Name("y"))
	as.Set("a", Enum(5))
	if len(as) != 2 {
		t.Fatalf("attrs = %+v", as)
	}
	if a, ok := as.Get("a"); !ok || a.String() != "5" {
		t.Errorf("a = %+v", a)
	}
	if _, ok := as.Get("zzz"); ok {
		t.Error("Get(zzz) ok")
	}
	b, _ := as.Get("b")
	if !reflect.DeepEqual(b.Strings(), []string{"x", "y"}) {
		t.Errorf("Strings = %v", b.Strings())
	}
	tests := []struct {
		a       Attribute
		str     string
		n       int
		nok     bool
		bv, bok bool
	}{
		{Attribute{Name: "empty"}, "", 0, false, false, false},
		{attr("i", Integer(7)), "7", 7, true, false, false},
		{attr("e", Enum(3)), "3", 3, true, false, false},
		{attr("b", Boolean(true)), "true", 0, false, true, true},
		{attr("k", Keyword("x")), "x", 0, false, false, false},
		{attr("nil", nil), "<nil>", 0, false, false, false},
	}
	for _, tt := range tests {
		if s := tt.a.String(); s != tt.str {
			t.Errorf("%s: String = %q, want %q", tt.a.Name, s, tt.str)
		}
		if n, ok := tt.a.Int(); n != tt.n || ok != tt.nok {
			t.Errorf("%s: Int = %d, %v", tt.a.Name, n, ok)
		}
		if v, ok := tt.a.Bool(); v != tt.bv || ok != tt.bok {
			t.Errorf("%s: Bool = %v, %v", tt.a.Name, v, ok)
		}
	}
}

func TestVersion(t *testing.T) {
	if v := Version11; v.Major() != 1 || v.Minor() != 1 || v.String() != "1.1" {
		t.Errorf("Version11 = %d.%d %q", v.Major(), v.Minor(), v)
	}
	if s := Version22.String(); s != "2.2" {
		t.Errorf("Version22 = %q", s)
	}
}

func TestEnumStrings(t *testing.T) {
	tests := []struct {
		v    interface{ String() string }
		want string
	}{
		{OpPrintJob, "Print-Job"},
		{OpCUPSGetPrinters, "CUPS-Get-Printers"},
		{Operation(0x1234), "operation(0x1234)"},
		{StatusOK, "successful-ok"},
		{StatusOKIgnoredOrSubstituted, "successful-ok-ignored-or-substituted-attributes"},
		{StatusErrorNotFound, "client-error-not-found"},
		{StatusErrorMultipleDocuments, "server-error-multiple-document-jobs-not-supported"},
		{Status(0x0777), "status(0x0777)"},
		{PrinterIdle, "idle"},
		{PrinterProcessing, "processing"},
		{PrinterStopped, "stopped"},
		{PrinterState(0), "printer-state(0)"},
		{JobPending, "pending"},
		{JobPendingHeld, "pending-held"},
		{JobProcessing, "processing"},
		{JobProcessingStopped, "processing-stopped"},
		{JobCanceled, "canceled"},
		{JobAborted, "aborted"},
		{JobCompleted, "completed"},
		{JobState(42), "job-state(42)"},
		{UnitsDPI, "dpi"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("%T(%v).String() = %q, want %q", tt.v, tt.v, got, tt.want)
		}
	}
	for _, s := range []JobState{JobCanceled, JobAborted, JobCompleted} {
		if !s.Terminal() {
			t.Errorf("%v not terminal", s)
		}
	}
	for _, s := range []JobState{JobPending, JobPendingHeld, JobProcessing, JobProcessingStopped} {
		if s.Terminal() {
			t.Errorf("%v terminal", s)
		}
	}
	if StatusOKConflicting.IsError() || !StatusErrorBadRequest.IsError() {
		t.Error("IsError wrong")
	}
}

func TestNewClientServers(t *testing.T) {
	tests := []struct {
		server, base, printerURI, socket string
	}{
		{"ipp://printer.local", "http://printer.local:631/", "ipp://printer.local:631/printers/q", ""},
		{"ipp://printer.local:8631/ipp/print", "http://printer.local:8631/ipp/print", "ipp://printer.local:8631/printers/q", ""},
		{"ipps://[::1]", "https://[::1]:631/", "ipps://[::1]:631/printers/q", ""},
		{"http://localhost:631", "http://localhost:631/", "ipp://localhost:631/printers/q", ""},
		{"https://user:pw@cups.example/?x#y", "https://cups.example/", "ipps://cups.example/printers/q", ""},
		{"/run/cups/cups.sock", "http://localhost/", "ipp://localhost/printers/q", "/run/cups/cups.sock"},
		{"unix:///var/run/cups/cups.sock", "http://localhost/", "ipp://localhost/printers/q", "/var/run/cups/cups.sock"},
		{"unix:/tmp/s", "http://localhost/", "ipp://localhost/printers/q", "/tmp/s"},
	}
	for _, tt := range tests {
		c, err := NewClient(tt.server)
		if err != nil {
			t.Errorf("NewClient(%q): %v", tt.server, err)
			continue
		}
		if got := c.base.String(); got != tt.base {
			t.Errorf("NewClient(%q) base = %q, want %q", tt.server, got, tt.base)
		}
		if got := c.PrinterURI("q"); got != tt.printerURI {
			t.Errorf("NewClient(%q) PrinterURI = %q, want %q", tt.server, got, tt.printerURI)
		}
		if c.socket != tt.socket {
			t.Errorf("NewClient(%q) socket = %q, want %q", tt.server, c.socket, tt.socket)
		}
		if c.http == nil || c.UserName() == "" {
			t.Errorf("NewClient(%q): http client %v, user %q", tt.server, c.http, c.UserName())
		}
	}
	for _, s := range []string{"", "localhost:631", "ftp://x", "ipp://", "http://%zz", "unix:", "unix://"} {
		if _, err := NewClient(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("NewClient(%q) err = %v, want ErrInvalid", s, err)
		}
	}
}

func TestUnixClientKeepsSettings(t *testing.T) {
	hc := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{MaxIdleConns: 7}}
	c, err := NewClient("/x.sock", WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	if c.http == hc || c.http.Timeout != 3*time.Second {
		t.Errorf("client not cloned with settings: %+v", c.http)
	}
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok || tr.MaxIdleConns != 7 || tr.DialContext == nil {
		t.Errorf("transport = %+v", c.http.Transport)
	}
	if hc.Transport.(*http.Transport).DialContext != nil {
		t.Error("caller's transport was modified")
	}
}

type fakeInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (f fakeInfo) Mode() fs.FileMode { return f.mode }

func TestCUPSServer(t *testing.T) {
	stat := func(files map[string]fs.FileMode) func(string) (fs.FileInfo, error) {
		return func(p string) (fs.FileInfo, error) {
			if m, ok := files[p]; ok {
				return fakeInfo{mode: m}, nil
			}
			return nil, fs.ErrNotExist
		}
	}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CUPS_SERVER" {
				return v
			}
			return ""
		}
	}
	tests := []struct {
		name  string
		files map[string]fs.FileMode
		env   string
		want  string
	}{
		{"run", map[string]fs.FileMode{"/run/cups/cups.sock": fs.ModeSocket, "/var/run/cups/cups.sock": fs.ModeSocket}, "", "/run/cups/cups.sock"},
		{"env wins over socket", map[string]fs.FileMode{"/run/cups/cups.sock": fs.ModeSocket}, "h", "ipp://h"},
		{"var run", map[string]fs.FileMode{"/var/run/cups/cups.sock": fs.ModeSocket}, "", "/var/run/cups/cups.sock"},
		{"macOS", map[string]fs.FileMode{"/private/var/run/cupsd": fs.ModeSocket}, "", "/private/var/run/cupsd"},
		{"not a socket", map[string]fs.FileMode{"/run/cups/cups.sock": 0}, "", "ipp://localhost:631"},
		{"env host", nil, "print.example", "ipp://print.example"},
		{"env host port version", nil, "print.example:8631/version=1.1", "ipp://print.example:8631"},
		{"env socket", nil, "/tmp/cups.sock", "/tmp/cups.sock"},
		{"env url", nil, "ipps://print.example", "ipps://print.example"},
		{"default", nil, "", "ipp://localhost:631"},
	}
	for _, tt := range tests {
		if got := cupsServer(stat(tt.files), env(tt.env)); got != tt.want {
			t.Errorf("%s: cupsServer = %q, want %q", tt.name, got, tt.want)
		}
	}
	// NewCUPSClient always yields a usable client.
	if c, err := NewCUPSClient(WithUserName("u")); err != nil || c.UserName() != "u" {
		t.Errorf("NewCUPSClient = %v, %v", c, err)
	}
}

func TestDefaultUserName(t *testing.T) {
	if defaultUserName() == "" {
		t.Error("empty default user name")
	}
}

func TestNextRequestID(t *testing.T) {
	c := &Client{}
	if id := c.nextRequestID(); id != 1 {
		t.Errorf("first id = %d", id)
	}
	c.lastID.Store(math.MaxInt32)
	if id := c.nextRequestID(); id != 1 {
		t.Errorf("id after wrap = %d", id)
	}
}
