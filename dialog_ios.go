//go:build ios

package goprint

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework UIKit -framework Foundation
#include <stdlib.h>
#include "dialog_ios.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// Results of the UIKit calls in dialog_ios.m.
const (
	iosDone     = 0 // printed, or a printer picked
	iosCanceled = 1
	iosFailed   = 2 // msg says why
	iosNoSheet  = 3 // UIKit could not show its sheet
)

type iosResult struct {
	status int
	msg    string
	info   iosPrintInfo
}

// iosCalls are the UIKit calls waiting for their completion handler, by
// id. UIKit may call a handler more than once; later calls are dropped.
var iosCalls = struct {
	sync.Mutex
	next uintptr
	m    map[uintptr]chan iosResult
}{m: map[uintptr]chan iosResult{}}

//export goprintIOSDone
func goprintIOSDone(id C.uintptr_t, status C.int, msg, printerID *C.char, duplex, orientation, outputType C.int) {
	iosCalls.Lock()
	ch, ok := iosCalls.m[uintptr(id)]
	delete(iosCalls.m, uintptr(id))
	iosCalls.Unlock()
	if !ok {
		return
	}
	ch <- iosResult{
		status: int(status),
		msg:    C.GoString(msg),
		info: iosPrintInfo{
			printerID:   C.GoString(printerID),
			duplex:      int(duplex),
			orientation: int(orientation),
			outputType:  int(outputType),
		},
	}
}

// iosCall starts a UIKit call on the main thread and waits for its
// completion handler. A canceled ctx stops the wait, not the sheet.
func iosCall(ctx context.Context, start func(id C.uintptr_t)) (iosResult, error) {
	if C.goprint_ios_is_main() != 0 {
		return iosResult{}, fmt.Errorf("%w: printing on iOS waits for UIKit's main thread; call it from another goroutine", ErrWrongThread)
	}
	ch := make(chan iosResult, 1)
	iosCalls.Lock()
	iosCalls.next++
	id := iosCalls.next
	iosCalls.m[id] = ch
	iosCalls.Unlock()
	start(C.uintptr_t(id))
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		return iosResult{}, ctx.Err()
	}
}

// iosError turns a result into the error for it.
func iosError(r iosResult) error {
	switch r.status {
	case iosDone:
		return nil
	case iosCanceled:
		return ErrCanceled
	case iosNoSheet:
		return fmt.Errorf("%w: %s", ErrNoDialog, r.msg)
	}
	if r.msg == "" {
		r.msg = "printing failed"
	}
	return errors.New("goprint: iOS: " + r.msg)
}

// cInfo holds the C strings of a print info until free.
type cInfo struct {
	jobName, printerID *C.char
	duplex, orient     C.int
	output             C.int
}

func newCInfo(i iosPrintInfo) cInfo {
	return cInfo{
		jobName: C.CString(i.jobName), printerID: C.CString(i.printerID),
		duplex: C.int(i.duplex), orient: C.int(i.orientation), output: C.int(i.outputType),
	}
}

func (c cInfo) free() {
	C.free(unsafe.Pointer(c.jobName))
	C.free(unsafe.Pointer(c.printerID))
}

// dialog shows UIKit's print sheet, or with opts.PrintNow unset only the
// printer picker. The Settings it returns name the printer by URL, which
// Print then takes without the sheet.
func (iosBackend) dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	if err := ctx.Err(); err != nil {
		return nil, Settings{}, err
	}
	s := opts.Settings
	if opts.RequirePrinter && !isPrinterURI(s.Printer) {
		return nil, Settings{}, iosNoPrinter(s.Printer)
	}
	if !opts.PrintNow {
		r, err := iosCall(ctx, func(id C.uintptr_t) {
			pid := C.CString(s.Printer)
			defer C.free(unsafe.Pointer(pid))
			C.goprint_ios_pick(id, pid, C.uintptr_t(opts.Owner))
		})
		if err == nil {
			err = iosError(r)
		}
		if err != nil {
			return nil, Settings{}, err
		}
		s.Printer = r.info.printerID
		return nil, s, nil
	}

	pdf, err := readPDF(doc)
	if err != nil {
		return nil, Settings{}, err
	}
	info, warnings := toIOSPrintInfo(s, docTitle(doc))
	if s.Strict && len(warnings) > 0 {
		return nil, Settings{}, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	r, err := iosCall(ctx, func(id C.uintptr_t) {
		ci := newCInfo(info)
		defer ci.free()
		C.goprint_ios_present(id, unsafe.Pointer(&pdf[0]), C.size_t(len(pdf)),
			ci.jobName, ci.printerID, ci.duplex, ci.orient, ci.output, C.uintptr_t(opts.Owner))
	})
	if err == nil {
		err = iosError(r)
	}
	if err != nil {
		return nil, Settings{}, err
	}
	return &Job{b: iosJob{}, warnings: warnings}, fromIOSPrintInfo(s, r.info), nil
}

// iosPrintToPrinter prints pdf to the printer at info.printerID without
// the sheet.
func iosPrintToPrinter(ctx context.Context, pdf []byte, info iosPrintInfo) error {
	if len(pdf) == 0 {
		return fmt.Errorf("%w: empty document", ErrInvalid)
	}
	r, err := iosCall(ctx, func(id C.uintptr_t) {
		ci := newCInfo(info)
		defer ci.free()
		C.goprint_ios_print_to(id, unsafe.Pointer(&pdf[0]), C.size_t(len(pdf)),
			ci.jobName, ci.printerID, ci.duplex, ci.orient, ci.output)
	})
	if err != nil {
		return err
	}
	return iosError(r)
}
