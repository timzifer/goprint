//go:build (linux && !android) || freebsd || openbsd || netbsd || dragonfly

// Package portal talks to the print portal of xdg-desktop-portal
// (org.freedesktop.portal.Print) over D-Bus. The portal shows the print
// dialog of the desktop environment (GNOME, KDE, ...) and prints a document
// passed as file descriptor; it works inside Flatpak and Snap sandboxes.
package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/timzifer/goprint/internal/errdefs"
)

const (
	busName    = "org.freedesktop.portal.Desktop"
	objectPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	printIface = "org.freedesktop.portal.Print"
	reqIface   = "org.freedesktop.portal.Request"
)

// Response codes of org.freedesktop.portal.Request.Response.
const (
	responseSuccess   = 0
	responseCancelled = 1
)

// Client is a connection to the session bus.
type Client struct {
	conn *dbus.Conn
}

// Connect opens a private connection to the session bus. It fails with
// errdefs.ErrNoDialog if there is no session bus or no print portal.
func Connect() (*Client, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: no session bus: %v", errdefs.ErrNoDialog, err)
	}
	c := &Client{conn: conn}
	if err := c.checkPortal(); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) checkPortal() error {
	var v dbus.Variant
	err := c.conn.Object(busName, objectPath).Call("org.freedesktop.DBus.Properties.Get", 0, printIface, "version").Store(&v)
	if err != nil {
		return fmt.Errorf("%w: print portal not available: %v", errdefs.ErrNoDialog, err)
	}
	return nil
}

// Settings are GTK print settings (all values are strings, as in
// GtkPrintSettings) and a GTK page setup.
type Settings struct {
	Print     map[string]string
	PageSetup map[string]dbus.Variant
}

// PrepareResult is the outcome of PreparePrint.
type PrepareResult struct {
	Settings
	// Token authorizes a following Print without showing the dialog again.
	Token uint32
}

// PreparePrint shows the print dialog preset with s. parent is the
// "x11:<hex>" or "wayland:<handle>" window identifier, or "".
// It returns errdefs.ErrCanceled if the user cancels.
func (c *Client) PreparePrint(ctx context.Context, parent, title string, s Settings) (*PrepareResult, error) {
	settings := map[string]dbus.Variant{}
	for k, v := range s.Print {
		settings[k] = dbus.MakeVariant(v)
	}
	pageSetup := s.PageSetup
	if pageSetup == nil {
		pageSetup = map[string]dbus.Variant{}
	}
	results, err := c.request(ctx, "PreparePrint", func(opts map[string]dbus.Variant) []any {
		opts["modal"] = dbus.MakeVariant(true)
		return []any{parent, title, settings, pageSetup, opts}
	})
	if err != nil {
		return nil, err
	}
	res := &PrepareResult{Settings: Settings{Print: map[string]string{}, PageSetup: map[string]dbus.Variant{}}}
	if v, ok := results["settings"]; ok {
		if m, ok := v.Value().(map[string]dbus.Variant); ok {
			for k, sv := range m {
				res.Print[k] = variantString(sv)
			}
		}
	}
	if v, ok := results["page-setup"]; ok {
		if m, ok := v.Value().(map[string]dbus.Variant); ok {
			res.PageSetup = m
		}
	}
	if v, ok := results["token"]; ok {
		if t, ok := v.Value().(uint32); ok {
			res.Token = t
		}
	}
	return res, nil
}

// Print hands the document to the portal, authorized by token from
// PreparePrint. The file is read by the portal through its descriptor.
func (c *Client) Print(ctx context.Context, parent, title string, doc *os.File, token uint32) error {
	_, err := c.request(ctx, "Print", func(opts map[string]dbus.Variant) []any {
		opts["token"] = dbus.MakeVariant(token)
		opts["modal"] = dbus.MakeVariant(true)
		return []any{parent, title, dbus.UnixFD(doc.Fd()), opts}
	})
	return err
}

// request calls a portal method that answers through a Request object and
// waits for its Response signal.
func (c *Client) request(ctx context.Context, method string, args func(opts map[string]dbus.Variant) []any) (map[string]dbus.Variant, error) {
	token, err := handleToken()
	if err != nil {
		return nil, err
	}
	// Subscribe before calling to not miss a fast response: the request
	// path is predictable from our unique name and the handle token.
	path := requestPath(c.conn.Names()[0], token)
	if err := c.conn.AddMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(reqIface), dbus.WithMatchMember("Response")); err != nil {
		return nil, err
	}
	defer c.conn.RemoveMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(reqIface), dbus.WithMatchMember("Response"))
	signals := make(chan *dbus.Signal, 4)
	c.conn.Signal(signals)
	defer c.conn.RemoveSignal(signals)

	opts := map[string]dbus.Variant{"handle_token": dbus.MakeVariant(token)}
	var handle dbus.ObjectPath
	call := c.conn.Object(busName, objectPath).CallWithContext(ctx, printIface+"."+method, 0, args(opts)...)
	if err := call.Store(&handle); err != nil {
		var de dbus.Error
		if errors.As(err, &de) && strings.HasSuffix(de.Name, ".ServiceUnknown") {
			return nil, fmt.Errorf("%w: %v", errdefs.ErrNoDialog, err)
		}
		return nil, fmt.Errorf("portal %s: %w", method, err)
	}
	for {
		select {
		case <-ctx.Done():
			c.conn.Object(busName, handle).Call(reqIface+".Close", 0)
			return nil, ctx.Err()
		case sig, ok := <-signals:
			if !ok {
				return nil, fmt.Errorf("portal %s: connection closed", method)
			}
			if sig.Path != handle || sig.Name != reqIface+".Response" || len(sig.Body) < 2 {
				continue
			}
			code, _ := sig.Body[0].(uint32)
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			switch code {
			case responseSuccess:
				return results, nil
			case responseCancelled:
				return nil, errdefs.ErrCanceled
			default:
				return nil, fmt.Errorf("portal %s: request failed (response %d)", method, code)
			}
		}
	}
}

func handleToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "goprint_" + hex.EncodeToString(b), nil
}

// requestPath derives the Request object path the portal will use:
// /org/freedesktop/portal/desktop/request/SENDER/TOKEN, where SENDER is the
// caller's unique name without the leading ':' and with '.' replaced by '_'.
func requestPath(uniqueName, token string) dbus.ObjectPath {
	sender := strings.ReplaceAll(strings.TrimPrefix(uniqueName, ":"), ".", "_")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
}

func variantString(v dbus.Variant) string {
	if s, ok := v.Value().(string); ok {
		return s
	}
	return fmt.Sprint(v.Value())
}
