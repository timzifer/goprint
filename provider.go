package goprint

import (
	"context"
	"errors"
	"fmt"
)

// Provider is a source of printers: the platform's print system
// ([System]), or anything else that can list printers and print, such as
// a simulated printer. A [Client] combines several providers.
//
// The Client validates documents and settings before it calls a provider,
// and sets [Printer.Provider] and [Settings.Provider] to the provider's
// name; providers need not do either.
type Provider interface {
	// Name identifies the provider in [Printer.Provider] and
	// [Settings.Provider]. It is unique within a Client; "" is reserved
	// for [System].
	Name() string
	// Printers lists the provider's printers.
	Printers(ctx context.Context) ([]Printer, error)
	// Capabilities reports the capabilities of the named printer. An empty
	// name refers to the provider's default printer.
	Capabilities(ctx context.Context, printer string) (Capabilities, error)
	// Print prints doc with s, without any UI. s.Printer names one of the
	// provider's printers; empty means its default printer.
	Print(ctx context.Context, doc Document, s Settings) (*Job, error)
}

// DialogProvider is a [Provider] with its own print dialog.
type DialogProvider interface {
	Provider
	// Dialog shows the print dialog as described at [Client.Dialog].
	Dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error)
}

// PropertiesProvider is a [Provider] whose printers have a driver
// settings dialog.
type PropertiesProvider interface {
	Provider
	// Properties shows the driver dialog as described at
	// [Client.PrinterProperties].
	Properties(ctx context.Context, s Settings, owner uintptr) (Settings, error)
}

// System returns the provider for the platform's print system: CUPS on
// Linux/BSD and macOS, the spooler on Windows, and IPP printers given by
// URI. Its name is "". It has the native dialog and, on Windows, the
// driver dialog.
func System() Provider { return systemProvider{platform} }

type systemProvider struct{ b backend }

func (systemProvider) Name() string { return "" }

func (p systemProvider) Printers(ctx context.Context) ([]Printer, error) {
	return p.b.printers(ctx)
}

func (p systemProvider) Capabilities(ctx context.Context, printer string) (Capabilities, error) {
	return p.b.capabilities(ctx, printer)
}

func (p systemProvider) Print(ctx context.Context, doc Document, s Settings) (*Job, error) {
	src, err := doc.open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return p.b.print(ctx, src, doc, s)
}

func (p systemProvider) Dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	// No native dialog passes attributes on.
	if len(doc.Attributes) > 0 && opts.Settings.Strict {
		return nil, Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, attributesWarning("print dialogs do not pass them on"))
	}
	job, s, err := p.b.dialog(ctx, doc, opts)
	if job != nil && len(doc.Attributes) > 0 {
		job.warnings = append(job.warnings, attributesWarning("print dialogs do not pass them on"))
	}
	return job, s, err
}

func (p systemProvider) Properties(ctx context.Context, s Settings, owner uintptr) (Settings, error) {
	return p.b.properties(ctx, s, owner)
}

// Client prints through a set of providers. [Settings.Provider] selects
// the provider for a call. The package-level functions use [Default].
type Client struct {
	providers []Provider
}

// Default is the Client behind the package-level functions. It has only
// the [System] provider. Programs that add providers replace it before
// they print, e.g. goprint.Default = goprint.NewClient(goprint.System(), p).
var Default = NewClient(System())

// NewClient returns a Client for providers, in the order given;
// [Client.Printers] lists their printers in that order. It panics if two
// providers have the same name.
func NewClient(providers ...Provider) *Client {
	seen := make(map[string]bool, len(providers))
	for _, p := range providers {
		if seen[p.Name()] {
			panic(fmt.Sprintf("goprint: duplicate provider %s", providerLabel(p.Name())))
		}
		seen[p.Name()] = true
	}
	return &Client{providers: append([]Provider(nil), providers...)}
}

// Providers returns the client's providers in order.
func (c *Client) Providers() []Provider {
	return append([]Provider(nil), c.providers...)
}

// provider returns the provider with the given name.
func (c *Client) provider(name string) (Provider, error) {
	for _, p := range c.providers {
		if p.Name() == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: no provider %s", ErrPrinterNotFound, providerLabel(name))
}

// providerLabel names a provider in messages.
func providerLabel(name string) string {
	if name == "" {
		return "system"
	}
	return fmt.Sprintf("%q", name)
}

// Printers lists the printers of all providers. If some providers fail,
// it returns the printers of the others together with an error that
// names the failed providers.
func (c *Client) Printers(ctx context.Context) ([]Printer, error) {
	var all []Printer
	var errs []error
	for _, p := range c.providers {
		ps, err := p.Printers(ctx)
		if err != nil {
			if len(c.providers) > 1 {
				err = fmt.Errorf("provider %s: %w", providerLabel(p.Name()), err)
			}
			errs = append(errs, err)
			continue
		}
		for _, pr := range ps {
			pr.Provider = p.Name()
			all = append(all, pr)
		}
	}
	return all, errors.Join(errs...)
}

// Capabilities reports the capabilities of the named printer of the named
// provider. An empty printer name refers to the provider's default printer.
func (c *Client) Capabilities(ctx context.Context, provider, printer string) (Capabilities, error) {
	p, err := c.provider(provider)
	if err != nil {
		return Capabilities{}, err
	}
	return p.Capabilities(ctx, printer)
}

// Print prints doc with s through the provider s.Provider, without any UI.
func (c *Client) Print(ctx context.Context, doc Document, s Settings) (*Job, error) {
	if err := doc.validate(); err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	p, err := c.provider(s.Provider)
	if err != nil {
		return nil, err
	}
	return p.Print(ctx, doc, s)
}

// Dialog shows the print dialog of the provider opts.Settings.Provider,
// preset with opts.Settings, and returns the job (if printed) together
// with the settings the user chose. It returns [ErrCanceled] if the user
// cancels and [ErrNoDialog] if the provider has no dialog available.
func (c *Client) Dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	if err := doc.validate(); err != nil {
		return nil, Settings{}, err
	}
	if err := opts.Settings.validate(); err != nil {
		return nil, Settings{}, err
	}
	p, err := c.provider(opts.Settings.Provider)
	if err != nil {
		return nil, Settings{}, err
	}
	dp, ok := p.(DialogProvider)
	if !ok {
		return nil, Settings{}, fmt.Errorf("%w: provider %s has no dialog", ErrNoDialog, providerLabel(p.Name()))
	}
	job, chosen, err := dp.Dialog(ctx, doc, opts)
	if err == nil {
		chosen.Provider = p.Name()
	}
	return job, chosen, err
}

// PrinterProperties shows the driver settings dialog of s.Printer at the
// provider s.Provider, as described at the package-level
// [PrinterProperties]. Providers without one return [ErrUnsupported].
func (c *Client) PrinterProperties(ctx context.Context, s Settings, owner uintptr) (Settings, error) {
	if err := s.validate(); err != nil {
		return Settings{}, err
	}
	p, err := c.provider(s.Provider)
	if err != nil {
		return Settings{}, err
	}
	pp, ok := p.(PropertiesProvider)
	if !ok {
		return Settings{}, fmt.Errorf("%w: provider %s has no driver dialog", ErrUnsupported, providerLabel(p.Name()))
	}
	out, err := pp.Properties(ctx, s, owner)
	if err != nil {
		return Settings{}, err
	}
	out.Provider = p.Name()
	return out, nil
}
