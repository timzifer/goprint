package ipp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// ContentType is the MIME type of IPP messages over HTTP.
const ContentType = "application/ipp"

// DefaultPort is the IANA port of IPP, used for ipp:// and ipps:// URLs
// without an explicit port.
const DefaultPort = "631"

// Credentials authenticate requests with HTTP Basic authentication.
type Credentials struct {
	Username string
	Password string
}

// Client sends IPP requests over HTTP/1.1. It is safe for concurrent use.
type Client struct {
	base      url.URL // http or https URL of the server
	uriScheme string  // ipp or ipps, for printer URIs built from names
	socket    string  // unix socket path, or ""
	http      *http.Client
	creds     *Credentials
	user      string
	lastID    atomic.Int32
}

// Option configures a [Client].
type Option func(*Client)

// WithHTTPClient sets the HTTP client used to send requests. For a unix
// socket server, a *http.Transport of hc (or a new one) is cloned and its
// dialer replaced, so that proxy and TLS settings are kept.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.http = hc }
}

// WithCredentials enables HTTP Basic authentication. Unless
// [WithUserName] is given, username is also sent as requesting-user-name.
func WithCredentials(username, password string) Option {
	return func(c *Client) { c.creds = &Credentials{Username: username, Password: password} }
}

// WithUserName sets the requesting-user-name operation attribute. The
// default is the current OS user, falling back to $USER, $USERNAME and
// $LOGNAME.
func WithUserName(name string) Option {
	return func(c *Client) { c.user = name }
}

// NewClient returns a client for the IPP server at server, which is one of
//
//   - an ipp:// or ipps:// URL (port 631 unless given),
//   - an http:// or https:// URL,
//   - a unix socket, given as an absolute path or a unix:// URL.
//
// A path in the URL is used for requests without a printer-uri or job-uri
// operation attribute; otherwise the path of that URI is used.
func NewClient(server string, opts ...Option) (*Client, error) {
	c := &Client{}
	if err := c.parseServer(server); err != nil {
		return nil, err
	}
	for _, o := range opts {
		o(c)
	}
	if c.user == "" && c.creds != nil {
		c.user = c.creds.Username
	}
	if c.user == "" {
		c.user = defaultUserName()
	}
	if c.socket != "" {
		c.http = unixClient(c.http, c.socket)
	} else if c.http == nil {
		c.http = &http.Client{}
	}
	return c, nil
}

func (c *Client) parseServer(s string) error {
	if p, ok := socketPath(s); ok {
		if p == "" {
			return invalidf("empty unix socket path in %q", s)
		}
		c.socket = p
		c.base = url.URL{Scheme: "http", Host: "localhost", Path: "/"}
		c.uriScheme = "ipp"
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: server %q: %w", ErrInvalid, s, err)
	}
	if u.Host == "" {
		return invalidf("server %q has no host", s)
	}
	switch u.Scheme {
	case "ipp", "ipps":
		if u.Port() == "" {
			u.Host = net.JoinHostPort(u.Hostname(), DefaultPort)
		}
		c.uriScheme = u.Scheme
		u.Scheme = map[string]string{"ipp": "http", "ipps": "https"}[u.Scheme]
	case "http":
		c.uriScheme = "ipp"
	case "https":
		c.uriScheme = "ipps"
	default:
		return invalidf("server %q: unsupported scheme %q", s, u.Scheme)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	c.base = *u
	return nil
}

// socketPath reports whether s names a unix socket and returns its path.
func socketPath(s string) (string, bool) {
	if p, ok := strings.CutPrefix(s, "unix:"); ok {
		return strings.TrimPrefix(p, "//"), true
	}
	if strings.HasPrefix(s, "/") || filepath.IsAbs(s) {
		return s, true
	}
	return "", false
}

func unixClient(hc *http.Client, path string) *http.Client {
	var t *http.Transport
	var nc http.Client
	if hc != nil {
		nc = *hc
		if ht, ok := hc.Transport.(*http.Transport); ok {
			t = ht.Clone()
		}
	}
	if t == nil {
		t = http.DefaultTransport.(*http.Transport).Clone()
	}
	t.Proxy = nil
	t.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
	nc.Transport = t
	return &nc
}

// CUPS socket locations, in discovery order.
var cupsSockets = []string{
	"/run/cups/cups.sock",
	"/var/run/cups/cups.sock",
	"/private/var/run/cupsd", // macOS
}

// NewCUPSClient returns a client for the CUPS scheduler. As with libcups,
// $CUPS_SERVER (a socket path, URL or host[:port]) wins if set; otherwise the
// server is the first existing unix socket of /run/cups/cups.sock,
// /var/run/cups/cups.sock and /private/var/run/cupsd (macOS), or
// localhost:631.
func NewCUPSClient(opts ...Option) (*Client, error) {
	return NewClient(cupsServer(os.Stat, os.Getenv), opts...)
}

func cupsServer(stat func(string) (fs.FileInfo, error), getenv func(string) string) string {
	if s := getenv("CUPS_SERVER"); s != "" {
		if strings.HasPrefix(s, "/") || strings.Contains(s, "://") {
			return s
		}
		// host[:port][/version=1.1]
		s, _, _ = strings.Cut(s, "/")
		return "ipp://" + s
	}
	for _, p := range cupsSockets {
		if fi, err := stat(p); err == nil && fi.Mode()&fs.ModeSocket != 0 {
			return p
		}
	}
	return "ipp://localhost:" + DefaultPort
}

func defaultUserName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndexByte(name, '\\'); i >= 0 { // DOMAIN\user
			name = name[i+1:]
		}
		return name
	}
	for _, k := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "anonymous"
}

// UserName returns the requesting-user-name sent with requests.
func (c *Client) UserName() string { return c.user }

// PrinterURI returns the printer URI of the CUPS queue name on this
// client's server, e.g. "ipp://localhost:631/printers/Office".
func (c *Client) PrinterURI(name string) string {
	return c.uriScheme + "://" + c.base.Host + "/printers/" + url.PathEscape(name)
}

// resolvePrinter returns printer unchanged if it is a URI and the printer
// URI of the queue name otherwise.
func (c *Client) resolvePrinter(printer string) string {
	if strings.Contains(printer, "://") {
		return printer
	}
	return c.PrinterURI(printer)
}

func (c *Client) nextRequestID() int32 {
	for {
		id := c.lastID.Add(1)
		if id > 0 {
			return id
		}
		c.lastID.CompareAndSwap(id, 0) // wrapped around; request-id must be positive
	}
}

// Do sends req with optional document data and returns the response. It
// sets req.RequestID to a fresh id and req.Version to 2.0 if it is zero.
// doc is streamed after the encoded request.
//
// The request is posted to the path of the printer-uri (or job-uri)
// operation attribute, or to the client's base path if there is none.
//
// If the response status is an error (>= 0x0400), Do returns the response
// together with a *[StatusError]. Successful statuses such as
// [StatusOKIgnoredOrSubstituted] are not errors; see [Message.Unsupported].
// An HTTP 401 response is reported as a *StatusError with
// [StatusErrorNotAuthenticated].
func (c *Client) Do(ctx context.Context, req *Message, doc io.Reader) (*Message, error) {
	if req.Version == 0 {
		req.Version = Version20
	}
	req.RequestID = c.nextRequestID()
	op := req.Operation()
	enc, err := req.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("ipp: %s: %w", op, err)
	}
	var body io.Reader = bytes.NewReader(enc)
	if doc != nil {
		body = io.MultiReader(body, doc)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(req), body)
	if err != nil {
		return nil, fmt.Errorf("ipp: %s: %w", op, err)
	}
	hreq.Header.Set("Content-Type", ContentType)
	if c.creds != nil {
		hreq.SetBasicAuth(c.creds.Username, c.creds.Password)
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("ipp: %s: %w", op, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // allow connection reuse
		resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, &StatusError{Code: StatusErrorNotAuthenticated, Message: "HTTP " + resp.Status}
	default:
		return nil, fmt.Errorf("ipp: %s: unexpected HTTP status %s", op, resp.Status)
	}
	m, err := Decode(bufio.NewReader(resp.Body))
	if err != nil {
		return nil, fmt.Errorf("ipp: %s response: %w", op, err)
	}
	if st := m.Status(); st.IsError() {
		return m, &StatusError{Code: st, Message: m.StatusMessage()}
	}
	return m, nil
}

// endpoint returns the HTTP URL to post req to.
func (c *Client) endpoint(req *Message) string {
	u := c.base
	if g := req.Group(TagOperationGroup); g != nil {
		for _, name := range []string{"printer-uri", "job-uri"} {
			a, ok := g.Attrs.Get(name)
			if !ok {
				continue
			}
			if pu, err := url.Parse(a.String()); err == nil && pu.Path != "" {
				u.Path, u.RawPath = pu.Path, pu.RawPath
				break
			}
		}
	}
	return u.String()
}
