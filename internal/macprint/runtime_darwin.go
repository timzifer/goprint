//go:build darwin

package macprint

import (
	"errors"
	"fmt"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Framework binaries. Since macOS 11 they live in the dyld shared cache,
// but dlopen still resolves these paths.
const (
	libSystem  = "/usr/lib/libSystem.B.dylib"
	foundation = "/System/Library/Frameworks/Foundation.framework/Foundation"
	appKit     = "/System/Library/Frameworks/AppKit.framework/AppKit"
	printCore  = "/System/Library/Frameworks/ApplicationServices.framework/Frameworks/PrintCore.framework/PrintCore"
)

// PDFKit moved out of Quartz.framework in macOS 10.13; the old path is a
// fallback for older systems.
var pdfKitPaths = []string{
	"/System/Library/Frameworks/PDFKit.framework/PDFKit",
	"/System/Library/Frameworks/Quartz.framework/Frameworks/PDFKit.framework/PDFKit",
}

// nsSize is NSSize/CGSize on 64-bit macOS: two CGFloat (double). It is
// passed and returned in floating-point registers on amd64 and arm64.
type nsSize struct {
	_    structs.HostLayout
	W, H float64
}

// Typed objc_msgSend variants for the calls that take or return a struct;
// objc.Send handles everything else.
var (
	msgSendSize    func(id objc.ID, sel objc.SEL, s nsSize)
	msgSendGetSize func(id objc.ID, sel objc.SEL) nsSize
)

// PrintCore (PMxxx) functions. OSStatus is int32, Boolean is a byte.
var (
	pmSetCopies                  func(ps uintptr, copies uint32, lock bool) int32
	pmSetCollate                 func(ps uintptr, collate bool) int32
	pmGetCollate                 func(ps uintptr, collate *bool) int32
	pmSetDuplex                  func(ps uintptr, mode uint32) int32
	pmGetDuplex                  func(ps uintptr, mode *uint32) int32
	pmPrintSettingsSetValue      func(ps uintptr, key, value objc.ID, locked bool) int32
	pmPrintSettingsGetValue      func(ps uintptr, key objc.ID, value *objc.ID) int32
	pmSessionGetCurrentPrinter   func(session uintptr, printer *uintptr) int32
	pmPrinterGetID               func(printer uintptr) objc.ID
	pmPrinterGetName             func(printer uintptr) objc.ID
	pmPrinterCreateFromPrinterID func(id objc.ID) uintptr
	pmRelease                    func(obj uintptr) int32
)

// pthreadMainNP is pthread_main_np, which [NSThread isMainThread] is built
// on. It needs neither Foundation nor AppKit.
var pthreadMainNP func() int32

// AppKit string constants, read from the framework so that their values
// never have to be guessed.
var (
	kPrintJobDisposition objc.ID // NSPrintJobDisposition
	kPrintSaveJob        objc.ID // NSPrintSaveJob
	kPrintJobSavingURL   objc.ID // NSPrintJobSavingURL
	kPrintCopies         objc.ID // NSPrintCopies
	kPrintAllPages       objc.ID // NSPrintAllPages
	kPrintFirstPage      objc.ID // NSPrintFirstPage
	kPrintLastPage       objc.ID // NSPrintLastPage
)

// Classes, resolved after the frameworks are loaded.
var (
	clsNSAutoreleasePool objc.Class
	clsNSString          objc.Class
	clsNSNumber          objc.Class
	clsNSData            objc.Class
	clsNSURL             objc.Class
	clsNSApplication     objc.Class
	clsNSPrintInfo       objc.Class
	clsNSPrinter         objc.Class
	clsNSPrintPanel      objc.Class
	clsPDFDocument       objc.Class
)

var (
	loadOnce sync.Once
	errLoad  error
)

// load opens the frameworks and resolves symbols and classes once.
func load() error {
	loadOnce.Do(func() { errLoad = doLoad() })
	return errLoad
}

func doLoad() error {
	open := func(path string) (uintptr, error) {
		h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return 0, fmt.Errorf("macprint: loading %s: %w", path, err)
		}
		return h, nil
	}
	libobjc, err := open("/usr/lib/libobjc.A.dylib")
	if err != nil {
		return err
	}
	if _, err := open(foundation); err != nil {
		return err
	}
	ak, err := open(appKit)
	if err != nil {
		return err
	}
	pdfLoaded := false
	for _, p := range pdfKitPaths {
		if _, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
			pdfLoaded = true
			break
		}
	}
	if !pdfLoaded {
		return errors.New("macprint: PDFKit not found")
	}
	pc, err := open(printCore)
	if err != nil {
		return err
	}

	// purego.RegisterLibFunc panics on missing symbols; resolve first so a
	// missing symbol becomes an error instead.
	bind := func(lib uintptr, fptr any, name string) error {
		addr, err := purego.Dlsym(lib, name)
		if err != nil {
			return fmt.Errorf("macprint: %s: %w", name, err)
		}
		purego.RegisterFunc(fptr, addr)
		return nil
	}
	for _, f := range []struct {
		lib  uintptr
		fptr any
		name string
	}{
		{libobjc, &msgSendSize, "objc_msgSend"},
		{libobjc, &msgSendGetSize, "objc_msgSend"},
		{pc, &pmSetCopies, "PMSetCopies"},
		{pc, &pmSetCollate, "PMSetCollate"},
		{pc, &pmGetCollate, "PMGetCollate"},
		{pc, &pmSetDuplex, "PMSetDuplex"},
		{pc, &pmGetDuplex, "PMGetDuplex"},
		{pc, &pmPrintSettingsSetValue, "PMPrintSettingsSetValue"},
		{pc, &pmPrintSettingsGetValue, "PMPrintSettingsGetValue"},
		{pc, &pmSessionGetCurrentPrinter, "PMSessionGetCurrentPrinter"},
		{pc, &pmPrinterGetID, "PMPrinterGetID"},
		{pc, &pmPrinterGetName, "PMPrinterGetName"},
		{pc, &pmPrinterCreateFromPrinterID, "PMPrinterCreateFromPrinterID"},
		{pc, &pmRelease, "PMRelease"},
	} {
		if err := bind(f.lib, f.fptr, f.name); err != nil {
			return err
		}
	}

	for _, c := range []struct {
		dst  *objc.ID
		name string
	}{
		{&kPrintJobDisposition, "NSPrintJobDisposition"},
		{&kPrintSaveJob, "NSPrintSaveJob"},
		{&kPrintJobSavingURL, "NSPrintJobSavingURL"},
		{&kPrintCopies, "NSPrintCopies"},
		{&kPrintAllPages, "NSPrintAllPages"},
		{&kPrintFirstPage, "NSPrintFirstPage"},
		{&kPrintLastPage, "NSPrintLastPage"},
	} {
		addr, err := purego.Dlsym(ak, c.name)
		if err != nil {
			return fmt.Errorf("macprint: %s: %w", c.name, err)
		}
		// addr is the address of an `NSString *const` variable in AppKit.
		if *c.dst = *(*objc.ID)(ptrAt(addr)); *c.dst == 0 {
			return fmt.Errorf("macprint: %s is nil", c.name)
		}
	}

	for _, c := range []struct {
		dst  *objc.Class
		name string
	}{
		{&clsNSAutoreleasePool, "NSAutoreleasePool"},
		{&clsNSString, "NSString"},
		{&clsNSNumber, "NSNumber"},
		{&clsNSData, "NSData"},
		{&clsNSURL, "NSURL"},
		{&clsNSApplication, "NSApplication"},
		{&clsNSPrintInfo, "NSPrintInfo"},
		{&clsNSPrinter, "NSPrinter"},
		{&clsNSPrintPanel, "NSPrintPanel"},
		{&clsPDFDocument, "PDFDocument"},
	} {
		if *c.dst = objc.GetClass(c.name); *c.dst == 0 {
			return fmt.Errorf("macprint: class %s not found", c.name)
		}
	}
	return nil
}

var (
	mainOnce sync.Once
	errMain  error
)

// loadMainCheck binds pthread_main_np.
func loadMainCheck() error {
	mainOnce.Do(func() {
		lib, err := purego.Dlopen(libSystem, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			errMain = fmt.Errorf("macprint: loading %s: %w", libSystem, err)
			return
		}
		addr, err := purego.Dlsym(lib, "pthread_main_np")
		if err != nil {
			errMain = fmt.Errorf("macprint: pthread_main_np: %w", err)
			return
		}
		purego.RegisterFunc(&pthreadMainNP, addr)
	})
	return errMain
}

// ptrAt turns an address of C memory (outside the Go heap) into a pointer.
// Converting through a pointer to the uintptr keeps go vet's unsafeptr
// check quiet; the memory is never moved or freed.
func ptrAt(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

// Selectors.
var (
	selAlloc                  = objc.RegisterName("alloc")
	selInit                   = objc.RegisterName("init")
	selNew                    = objc.RegisterName("new")
	selRelease                = objc.RegisterName("release")
	selDrain                  = objc.RegisterName("drain")
	selUTF8String             = objc.RegisterName("UTF8String")
	selStringWithUTF8String   = objc.RegisterName("stringWithUTF8String:")
	selIsKindOfClass          = objc.RegisterName("isKindOfClass:")
	selNumberWithInteger      = objc.RegisterName("numberWithInteger:")
	selNumberWithBool         = objc.RegisterName("numberWithBool:")
	selIntegerValue           = objc.RegisterName("integerValue")
	selBoolValue              = objc.RegisterName("boolValue")
	selDataWithBytesLength    = objc.RegisterName("dataWithBytes:length:")
	selFileURLWithPath        = objc.RegisterName("fileURLWithPath:")
	selObjectForKey           = objc.RegisterName("objectForKey:")
	selSetObjectForKey        = objc.RegisterName("setObject:forKey:")
	selSharedApplication      = objc.RegisterName("sharedApplication")
	selIsRunning              = objc.RegisterName("isRunning")
	selActivationPolicy       = objc.RegisterName("activationPolicy")
	selSetActivationPolicy    = objc.RegisterName("setActivationPolicy:")
	selActivateIgnoringOthers = objc.RegisterName("activateIgnoringOtherApps:")
	selInitWithData           = objc.RegisterName("initWithData:")
	selPageCount              = objc.RegisterName("pageCount")
	selIsLocked               = objc.RegisterName("isLocked")
	selPrintOperation         = objc.RegisterName("printOperationForPrintInfo:scalingMode:autoRotate:")
	selPrinterWithName        = objc.RegisterName("printerWithName:")
	selSetPrinter             = objc.RegisterName("setPrinter:")
	selPrinter                = objc.RegisterName("printer")
	selName                   = objc.RegisterName("name")
	selPaperName              = objc.RegisterName("paperName")
	selPaperSize              = objc.RegisterName("paperSize")
	selSetPaperSize           = objc.RegisterName("setPaperSize:")
	selOrientation            = objc.RegisterName("orientation")
	selSetOrientation         = objc.RegisterName("setOrientation:")
	selDictionary             = objc.RegisterName("dictionary")
	selPMPrintSettings        = objc.RegisterName("PMPrintSettings")
	selPMPrintSession         = objc.RegisterName("PMPrintSession")
	selUpdateFromPMSettings   = objc.RegisterName("updateFromPMPrintSettings")
	selJobDisposition         = objc.RegisterName("jobDisposition")
	selPrintInfo              = objc.RegisterName("printInfo")
	selSetJobTitle            = objc.RegisterName("setJobTitle:")
	selSetShowsPrintPanel     = objc.RegisterName("setShowsPrintPanel:")
	selSetShowsProgressPanel  = objc.RegisterName("setShowsProgressPanel:")
	selPrintPanel             = objc.RegisterName("printPanel")
	selSetOptions             = objc.RegisterName("setOptions:")
	selRunOperation           = objc.RegisterName("runOperation")
	selRunModalWithPrintInfo  = objc.RegisterName("runModalWithPrintInfo:")
)

// nsString returns an autoreleased NSString (toll-free bridged to
// CFStringRef).
func nsString(s string) objc.ID {
	return objc.ID(clsNSString).Send(selStringWithUTF8String, s)
}

// goString converts an NSString to a Go string; nil and non-strings give "".
func goString(s objc.ID) string {
	if s == 0 || !objc.Send[bool](s, selIsKindOfClass, clsNSString) {
		return ""
	}
	p := objc.Send[*byte](s, selUTF8String)
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

func nsInteger(v int) objc.ID {
	return objc.ID(clsNSNumber).Send(selNumberWithInteger, v)
}

func nsBool(v bool) objc.ID {
	return objc.ID(clsNSNumber).Send(selNumberWithBool, v)
}

// autoreleasePool pushes a pool; call the returned function to drain it.
// The main thread has no pool of its own unless an app run loop is
// running, so every entry point wraps its work in one.
func autoreleasePool() func() {
	pool := objc.ID(clsNSAutoreleasePool).Send(selNew)
	return func() {
		if pool != 0 {
			pool.Send(selDrain)
		}
	}
}

// osErr turns a non-zero OSStatus into an error.
func osErr(fn string, st int32) error {
	if st == 0 {
		return nil
	}
	return fmt.Errorf("macprint: %s failed: OSStatus %d", fn, st)
}
