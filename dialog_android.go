//go:build android && cgo

package goprint

/*
#include <stdint.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"unsafe"
)

// Android prints through its PrintManager dialog; apps cannot list
// printers or print without it. Printers given by URL are reached over
// IPP directly.
var platform backend = androidBackend{}

type androidBackend struct{}

func (androidBackend) printers(context.Context) ([]Printer, error) { return nil, nil }

func (androidBackend) capabilities(ctx context.Context, printer string) (Capabilities, error) {
	if !isPrinterURI(printer) {
		return Capabilities{}, androidNoPrinter(printer)
	}
	return networkIPP.capabilities(ctx, printer)
}

func (androidBackend) print(ctx context.Context, src io.Reader, doc Document, s Settings) (*Job, error) {
	if !isPrinterURI(s.Printer) {
		return nil, androidNoPrinter(s.Printer)
	}
	return networkIPP.print(ctx, src, doc, s)
}

func (androidBackend) properties(context.Context, Settings, uintptr) (Settings, error) {
	return Settings{}, fmt.Errorf("%w: printers have no driver dialog on Android", ErrUnsupported)
}

func androidNoPrinter(name string) error {
	if name == "" {
		return fmt.Errorf("%w: Android prints without its dialog only to printer URLs (ipp://…)", ErrNoPrinter)
	}
	return fmt.Errorf("%w: %q: Android prints without its dialog only to printer URLs (ipp://…)", ErrPrinterNotFound, name)
}

// androidDone is what PDFAdapter reports when its dialog closes.
type androidDone struct {
	adapter uintptr // global reference, 0 if none
	result  androidResult
	err     string
}

// androidCalls are the dialogs waiting for their result, by id.
var androidCalls = struct {
	sync.Mutex
	next uintptr
	m    map[uintptr]chan androidDone
}{m: map[uintptr]chan androidDone{}}

//export goprintAndroidDone
func goprintAndroidDone(id, adapter C.uintptr_t, info *C.int, n C.int, msg *C.char) {
	d := androidDone{adapter: uintptr(adapter), err: C.GoString(msg)}
	if info != nil && n > 0 {
		for _, v := range unsafe.Slice(info, int(n)) {
			d.result = append(d.result, int(v))
		}
	}
	androidCalls.Lock()
	ch, ok := androidCalls.m[uintptr(id)]
	delete(androidCalls.m, uintptr(id))
	androidCalls.Unlock()
	if !ok {
		if d.adapter != 0 {
			go releaseAdapter(d.adapter)
		}
		return
	}
	ch <- d
}

func currentAndroidRunner() AndroidRunner {
	androidRunner.Lock()
	defer androidRunner.Unlock()
	return androidRunner.run
}

// withJava runs f through the installed AndroidRunner.
func withJava(f func(vm, env, activity uintptr) error) error {
	run := currentAndroidRunner()
	if run == nil {
		return fmt.Errorf("%w: no Android runner; call goprint.SetAndroidRunner (fyneprint does)", ErrNoDialog)
	}
	return run(func(vm, env, activity uintptr) error {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		return f(vm, env, activity)
	})
}

func releaseAdapter(adapter uintptr) {
	_ = withJava(func(_, env, _ uintptr) error {
		jniRelease(env, adapter)
		return nil
	})
}

// dialog shows Android's print dialog with the PDF. The dialog always
// prints (or saves a PDF); it cannot only pick a printer.
func (androidBackend) dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	if err := ctx.Err(); err != nil {
		return nil, Settings{}, err
	}
	s := opts.Settings
	switch {
	case !opts.PrintNow:
		return nil, Settings{}, fmt.Errorf("%w: Android's print dialog always prints; it cannot only choose settings", ErrUnsupported)
	case opts.NoFileOutput:
		return nil, Settings{}, fmt.Errorf("%w: Android's print dialog always offers Save as PDF", ErrUnsupported)
	case opts.RequirePrinter && s.Printer != "":
		return nil, Settings{}, fmt.Errorf("%w: Android's print dialog cannot preselect %q (RequirePrinter)", ErrUnsupported, s.Printer)
	}
	attrs, warnings := toAndroidAttrs(s)
	if s.Strict && len(warnings) > 0 {
		return nil, Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	pdf, err := readPDF(doc)
	if err != nil {
		return nil, Settings{}, err
	}
	if len(pdf) == 0 {
		return nil, Settings{}, fmt.Errorf("%w: empty document", ErrInvalid)
	}

	ch := make(chan androidDone, 1)
	androidCalls.Lock()
	androidCalls.next++
	id := androidCalls.next
	androidCalls.m[id] = ch
	androidCalls.Unlock()
	err = withJava(func(_, env, activity uintptr) error {
		if err := jniLoad(env, activity); err != nil {
			return err
		}
		return jniStart(env, activity, pdf, docTitle(doc), attrs, id)
	})
	if err != nil {
		androidCalls.Lock()
		delete(androidCalls.m, id)
		androidCalls.Unlock()
		return nil, Settings{}, err
	}

	var d androidDone
	select {
	case d = <-ch:
	case <-ctx.Done():
		return nil, Settings{}, ctx.Err()
	}
	state := androidCanceled
	if len(d.result) > 0 {
		state = d.result[0]
	}
	switch {
	case d.err != "":
		err = errors.New("goprint: Android: " + d.err)
	case state == androidCanceled:
		err = ErrCanceled
	case state == androidFailed:
		err = errors.New("goprint: Android: the print job failed")
	}
	if err != nil {
		if d.adapter != 0 {
			go releaseAdapter(d.adapter)
		}
		return nil, Settings{}, err
	}
	j := &androidJob{adapter: d.adapter}
	if d.adapter != 0 {
		runtime.AddCleanup(j, releaseAdapter, d.adapter)
	}
	return &Job{b: j, warnings: warnings}, d.result.settings(s), nil
}

// androidJob follows a job through PDFAdapter, which holds the PrintJob.
type androidJob struct {
	adapter uintptr
}

func (j *androidJob) id() string { return "" }

func (j *androidJob) state(context.Context) (JobState, error) {
	if j.adapter == 0 {
		return JobCompleted, nil
	}
	var st int
	err := withJava(func(_, env, _ uintptr) error {
		var err error
		st, err = jniState(env, j.adapter)
		return err
	})
	if err != nil {
		return JobPending, err
	}
	return androidJobState(st), nil
}

func (j *androidJob) wait(ctx context.Context) error {
	return pollJob(ctx, j.state, "Android print job")
}

func (j *androidJob) cancel(context.Context) error {
	if j.adapter == 0 {
		return fmt.Errorf("%w: the job cannot be canceled", ErrUnsupported)
	}
	return withJava(func(_, env, _ uintptr) error { return jniCancel(env, j.adapter) })
}
