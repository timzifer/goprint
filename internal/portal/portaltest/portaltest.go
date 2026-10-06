//go:build linux || freebsd || openbsd || netbsd || dragonfly

// Package portaltest runs a private D-Bus session with a fake
// org.freedesktop.portal.Print implementation for tests.
package portaltest

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Portal is a fake print portal. Configure it with SetResponse and
// SetChoose; it is called from D-Bus goroutines.
type Portal struct {
	mu   sync.Mutex
	conn *dbus.Conn

	// Response is the response code sent for PreparePrint (0 success,
	// 1 cancelled, 2 other). Set it with SetResponse.
	Response uint32
	// Choose, if set, turns the preset settings into the "user's choice".
	// By default the presets are returned unchanged. Set it with SetChoose.
	Choose func(settings map[string]string, pageSetup map[string]dbus.Variant) (map[string]string, map[string]dbus.Variant)

	// Recorded calls.
	Prepared  []Prepared
	Printed   []Printed
	tokenNext uint32
}

// Prepared records a PreparePrint call.
type Prepared struct {
	Parent, Title string
	Settings      map[string]string
	PageSetup     map[string]dbus.Variant
}

// Printed records a Print call.
type Printed struct {
	Parent, Title string
	Token         uint32
	Document      []byte
}

// Start launches a private dbus-daemon, points DBUS_SESSION_BUS_ADDRESS at
// it for the test and registers the fake portal. It skips the test when
// dbus-daemon is missing, unless GOPRINT_PORTAL_TEST requires it.
func Start(t *testing.T) *Portal {
	t.Helper()
	bin, err := exec.LookPath("dbus-daemon")
	if err != nil {
		if os.Getenv("GOPRINT_PORTAL_TEST") != "" {
			t.Fatalf("dbus-daemon not found: %v", err)
		}
		t.Skip("dbus-daemon not installed")
	}
	cmd := exec.Command(bin, "--session", "--nofork", "--print-address=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr, err := readLine(out, 10*time.Second)
	if err != nil {
		t.Fatalf("dbus-daemon address: %v", err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)

	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect fake portal: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	p := &Portal{conn: conn, tokenNext: 1}
	if err := conn.Export(p, "/org/freedesktop/portal/desktop", "org.freedesktop.portal.Print"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(props{}, "/org/freedesktop/portal/desktop", "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName("org.freedesktop.portal.Desktop", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request portal name: %v (%v)", err, reply)
	}
	return p
}

func readLine(r io.Reader, timeout time.Duration) (string, error) {
	ch := make(chan string, 1)
	errc := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			errc <- err
			return
		}
		ch <- strings.TrimSpace(line)
	}()
	select {
	case l := <-ch:
		return l, nil
	case err := <-errc:
		return "", err
	case <-time.After(timeout):
		return "", fmt.Errorf("timeout")
	}
}

type props struct{}

// Get implements org.freedesktop.DBus.Properties.Get for "version".
func (props) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface == "org.freedesktop.portal.Print" && name == "version" {
		return dbus.MakeVariant(uint32(3)), nil
	}
	return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("no property %s.%s", iface, name))
}

func requestPath(sender dbus.Sender, opts map[string]dbus.Variant) dbus.ObjectPath {
	token, _ := opts["handle_token"].Value().(string)
	s := strings.ReplaceAll(strings.TrimPrefix(string(sender), ":"), ".", "_")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + s + "/" + token)
}

// respond emits the Response signal asynchronously, after the method reply.
func (p *Portal) respond(path dbus.ObjectPath, code uint32, results map[string]dbus.Variant) {
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = p.conn.Emit(path, "org.freedesktop.portal.Request.Response", code, results)
	}()
}

// PreparePrint implements org.freedesktop.portal.Print.PreparePrint.
func (p *Portal) PreparePrint(sender dbus.Sender, parent, title string, settings, pageSetup, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	s := map[string]string{}
	for k, v := range settings {
		s[k], _ = v.Value().(string)
	}
	path := requestPath(sender, opts)
	p.mu.Lock()
	p.Prepared = append(p.Prepared, Prepared{Parent: parent, Title: title, Settings: s, PageSetup: pageSetup})
	code, choose := p.Response, p.Choose
	token := p.tokenNext
	p.tokenNext++
	p.mu.Unlock()

	if choose != nil {
		// Hand copies to Choose so the recorded presets stay unchanged.
		sc := make(map[string]string, len(s))
		for k, v := range s {
			sc[k] = v
		}
		pc := make(map[string]dbus.Variant, len(pageSetup))
		for k, v := range pageSetup {
			pc[k] = v
		}
		s, pageSetup = choose(sc, pc)
	}
	out := map[string]dbus.Variant{}
	for k, v := range s {
		out[k] = dbus.MakeVariant(v)
	}
	results := map[string]dbus.Variant{}
	if code == 0 {
		results = map[string]dbus.Variant{
			"settings":   dbus.MakeVariant(out),
			"page-setup": dbus.MakeVariant(pageSetup),
			"token":      dbus.MakeVariant(token),
		}
	}
	p.respond(path, code, results)
	return path, nil
}

// Print implements org.freedesktop.portal.Print.Print.
func (p *Portal) Print(sender dbus.Sender, parent, title string, fd dbus.UnixFD, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	f := os.NewFile(uintptr(fd), "document")
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	token, _ := opts["token"].Value().(uint32)
	path := requestPath(sender, opts)
	p.mu.Lock()
	p.Printed = append(p.Printed, Printed{Parent: parent, Title: title, Token: token, Document: data})
	p.mu.Unlock()
	p.respond(path, 0, map[string]dbus.Variant{})
	return path, nil
}

// Calls returns copies of the recorded calls.
func (p *Portal) Calls() ([]Prepared, []Printed) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Prepared(nil), p.Prepared...), append([]Printed(nil), p.Printed...)
}

// SetChoose sets the function that turns presets into the user's choice.
func (p *Portal) SetChoose(f func(settings map[string]string, pageSetup map[string]dbus.Variant) (map[string]string, map[string]dbus.Variant)) {
	p.mu.Lock()
	p.Choose = f
	p.mu.Unlock()
}

// SetResponse sets the PreparePrint response code.
func (p *Portal) SetResponse(code uint32) {
	p.mu.Lock()
	p.Response = code
	p.mu.Unlock()
}
