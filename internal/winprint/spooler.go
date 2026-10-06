//go:build windows

package winprint

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/errdefs"
)

var (
	modwinspool = windows.NewLazySystemDLL("winspool.drv")

	procEnumPrintersW     = modwinspool.NewProc("EnumPrintersW")
	procGetDefaultPrinter = modwinspool.NewProc("GetDefaultPrinterW")
	procOpenPrinterW      = modwinspool.NewProc("OpenPrinterW")
	procClosePrinter      = modwinspool.NewProc("ClosePrinter")
	procGetJobW           = modwinspool.NewProc("GetJobW")
	procSetJobW           = modwinspool.NewProc("SetJobW")
)

// PrinterInfo describes an installed printer.
type PrinterInfo struct {
	Name     string
	Comment  string
	Location string
	Driver   string
	Port     string
	Default  bool
}

// printerInfo2 mirrors PRINTER_INFO_2W.
type printerInfo2 struct {
	serverName, printerName, shareName, portName, driverName *uint16
	comment, location                                        *uint16
	devMode                                                  uintptr
	sepFile, printProcessor, datatype, parameters            *uint16
	securityDescriptor                                       uintptr
	attributes, priority, defaultPriority, startTime         uint32
	untilTime, status, jobs, averagePPM                      uint32
}

const (
	printerEnumLocal       = 0x2
	printerEnumConnections = 0x4
)

// Printers lists local printers and printer connections.
func Printers() ([]PrinterInfo, error) {
	def, _ := DefaultPrinter()
	var needed, returned uint32
	flags := uintptr(printerEnumLocal | printerEnumConnections)
	r, _, e := syscall.SyscallN(procEnumPrintersW.Addr(), flags, 0, 2, 0, 0, uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
	if r == 0 && e != windows.ERROR_INSUFFICIENT_BUFFER {
		if needed == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("EnumPrintersW: %w", e)
	}
	for {
		buf := make([]uint64, (needed+7)/8) // 8-byte aligned
		r, _, e = syscall.SyscallN(procEnumPrintersW.Addr(), flags, 0, 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(needed), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
		if r == 0 {
			if e == windows.ERROR_INSUFFICIENT_BUFFER {
				continue // the list grew in between
			}
			return nil, fmt.Errorf("EnumPrintersW: %w", e)
		}
		infos := unsafe.Slice((*printerInfo2)(unsafe.Pointer(&buf[0])), returned)
		out := make([]PrinterInfo, 0, returned)
		for _, in := range infos {
			name := windows.UTF16PtrToString(in.printerName)
			out = append(out, PrinterInfo{
				Name:     name,
				Comment:  windows.UTF16PtrToString(in.comment),
				Location: windows.UTF16PtrToString(in.location),
				Driver:   windows.UTF16PtrToString(in.driverName),
				Port:     windows.UTF16PtrToString(in.portName),
				Default:  name == def,
			})
		}
		return out, nil
	}
}

// DefaultPrinter returns the name of the default printer.
func DefaultPrinter() (string, error) {
	var n uint32
	syscall.SyscallN(procGetDefaultPrinter.Addr(), 0, uintptr(unsafe.Pointer(&n)))
	if n == 0 {
		return "", errdefs.ErrNoPrinter
	}
	buf := make([]uint16, n)
	r, _, e := syscall.SyscallN(procGetDefaultPrinter.Addr(), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		if e == windows.ERROR_FILE_NOT_FOUND {
			return "", errdefs.ErrNoPrinter
		}
		return "", fmt.Errorf("GetDefaultPrinterW: %w", e)
	}
	return windows.UTF16ToString(buf), nil
}

// printerHandle is an open spooler handle.
type printerHandle windows.Handle

func openPrinter(name string) (printerHandle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var h windows.Handle
	r, _, e := syscall.SyscallN(procOpenPrinterW.Addr(), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&h)), 0)
	if r == 0 {
		if e == windows.ERROR_INVALID_PRINTER_NAME {
			return 0, fmt.Errorf("%w: %q", errdefs.ErrPrinterNotFound, name)
		}
		return 0, fmt.Errorf("OpenPrinterW(%q): %w", name, e)
	}
	return printerHandle(h), nil
}

func (h printerHandle) Close() {
	syscall.SyscallN(procClosePrinter.Addr(), uintptr(h))
}

// jobInfo1 mirrors JOB_INFO_1W.
type jobInfo1 struct {
	jobID                                                          uint32
	printerName, machineName, userName, document, datatype, status *uint16
	statusFlags, priority, position, totalPages, pagesPrinted      uint32
	submitted                                                      windows.Systemtime
}

// Spooler job status flags (JOB_STATUS_*).
const (
	jobStatusPaused           = 0x1
	jobStatusError            = 0x2
	jobStatusDeleting         = 0x4
	jobStatusSpooling         = 0x8
	jobStatusPrinting         = 0x10
	jobStatusOffline          = 0x20
	jobStatusPaperOut         = 0x40
	jobStatusPrinted          = 0x80
	jobStatusDeleted          = 0x100
	jobStatusBlocked          = 0x200
	jobStatusUserIntervention = 0x400
	jobStatusComplete         = 0x1000
	jobStatusRetained         = 0x2000
)

// errJobGone means the spooler no longer knows the job.
var errJobGone = errors.New("winprint: job no longer in queue")

func (h printerHandle) job(id uint32) (status uint32, err error) {
	info, err := h.jobInfo(id)
	return info.statusFlags, err
}

// jobDocument returns the document name of a queued job.
func (h printerHandle) jobDocument(id uint32) (string, error) {
	info, err := h.jobInfo(id)
	if err != nil {
		return "", err
	}
	return windows.UTF16PtrToString(info.document), nil
}

func (h printerHandle) jobInfo(id uint32) (jobInfo1, error) {
	var needed uint32
	syscall.SyscallN(procGetJobW.Addr(), uintptr(h), uintptr(id), 1, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed == 0 {
		return jobInfo1{}, errJobGone
	}
	buf := make([]uint64, (needed+7)/8)
	r, _, e := syscall.SyscallN(procGetJobW.Addr(), uintptr(h), uintptr(id), 1, uintptr(unsafe.Pointer(&buf[0])), uintptr(needed), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		if e == windows.ERROR_INVALID_PARAMETER {
			return jobInfo1{}, errJobGone
		}
		return jobInfo1{}, fmt.Errorf("GetJobW: %w", e)
	}
	// The strings point into buf, which the returned copy keeps alive only
	// until the caller is done with it; callers convert them immediately.
	return *(*jobInfo1)(unsafe.Pointer(&buf[0])), nil
}

// findJobPrinter returns the printer whose queue holds job id with the given
// document name. Spooler job ids are unique per print server, the name
// guards against a reused id.
func findJobPrinter(id uint32, document string) (string, error) {
	ps, err := Printers()
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		h, err := openPrinter(p.Name)
		if err != nil {
			continue
		}
		doc, err := h.jobDocument(id)
		h.Close()
		if err == nil && doc == document {
			return p.Name, nil
		}
	}
	return "", errJobGone
}

const jobControlDelete = 5

func (h printerHandle) deleteJob(id uint32) error {
	r, _, e := syscall.SyscallN(procSetJobW.Addr(), uintptr(h), uintptr(id), 0, 0, jobControlDelete)
	if r == 0 && e != windows.ERROR_INVALID_PARAMETER {
		return fmt.Errorf("SetJobW(delete): %w", e)
	}
	return nil
}
