package ipp

import "fmt"

// Operation is an IPP operation-id.
type Operation uint16

// Operations (RFC 8011 §5.2.2, CUPS Implementation of IPP).
const (
	OpPrintJob             Operation = 0x0002
	OpPrintURI             Operation = 0x0003
	OpValidateJob          Operation = 0x0004
	OpCreateJob            Operation = 0x0005
	OpSendDocument         Operation = 0x0006
	OpSendURI              Operation = 0x0007
	OpCancelJob            Operation = 0x0008
	OpGetJobAttributes     Operation = 0x0009
	OpGetJobs              Operation = 0x000A
	OpGetPrinterAttributes Operation = 0x000B
	OpHoldJob              Operation = 0x000C
	OpReleaseJob           Operation = 0x000D
	OpRestartJob           Operation = 0x000E
	OpPausePrinter         Operation = 0x0010
	OpResumePrinter        Operation = 0x0011
	OpPurgeJobs            Operation = 0x0012
	OpCUPSGetDefault       Operation = 0x4001
	OpCUPSGetPrinters      Operation = 0x4002
)

var operationNames = map[Operation]string{
	OpPrintJob:             "Print-Job",
	OpPrintURI:             "Print-URI",
	OpValidateJob:          "Validate-Job",
	OpCreateJob:            "Create-Job",
	OpSendDocument:         "Send-Document",
	OpSendURI:              "Send-URI",
	OpCancelJob:            "Cancel-Job",
	OpGetJobAttributes:     "Get-Job-Attributes",
	OpGetJobs:              "Get-Jobs",
	OpGetPrinterAttributes: "Get-Printer-Attributes",
	OpHoldJob:              "Hold-Job",
	OpReleaseJob:           "Release-Job",
	OpRestartJob:           "Restart-Job",
	OpPausePrinter:         "Pause-Printer",
	OpResumePrinter:        "Resume-Printer",
	OpPurgeJobs:            "Purge-Jobs",
	OpCUPSGetDefault:       "CUPS-Get-Default",
	OpCUPSGetPrinters:      "CUPS-Get-Printers",
}

// String returns the operation name, e.g. "Print-Job".
func (op Operation) String() string {
	if s, ok := operationNames[op]; ok {
		return s
	}
	return fmt.Sprintf("operation(0x%04x)", uint16(op))
}

// Status is an IPP status-code.
type Status uint16

// Status codes (RFC 8011 §B.1).
const (
	StatusOK                         Status = 0x0000
	StatusOKIgnoredOrSubstituted     Status = 0x0001
	StatusOKConflicting              Status = 0x0002
	StatusErrorBadRequest            Status = 0x0400
	StatusErrorForbidden             Status = 0x0401
	StatusErrorNotAuthenticated      Status = 0x0402
	StatusErrorNotAuthorized         Status = 0x0403
	StatusErrorNotPossible           Status = 0x0404
	StatusErrorTimeout               Status = 0x0405
	StatusErrorNotFound              Status = 0x0406
	StatusErrorGone                  Status = 0x0407
	StatusErrorRequestEntityTooLarge Status = 0x0408
	StatusErrorRequestValueTooLong   Status = 0x0409
	StatusErrorDocumentFormat        Status = 0x040A // document-format-not-supported
	StatusErrorAttributesOrValues    Status = 0x040B // attributes-or-values-not-supported
	StatusErrorURIScheme             Status = 0x040C // uri-scheme-not-supported
	StatusErrorCharset               Status = 0x040D // charset-not-supported
	StatusErrorConflicting           Status = 0x040E // conflicting-attributes
	StatusErrorCompression           Status = 0x040F // compression-not-supported
	StatusErrorCompressionError      Status = 0x0410
	StatusErrorDocumentFormatError   Status = 0x0411
	StatusErrorDocumentAccess        Status = 0x0412
	StatusErrorInternal              Status = 0x0500
	StatusErrorOperationNotSupported Status = 0x0501
	StatusErrorServiceUnavailable    Status = 0x0502
	StatusErrorVersionNotSupported   Status = 0x0503
	StatusErrorDevice                Status = 0x0504
	StatusErrorTemporary             Status = 0x0505
	StatusErrorNotAcceptingJobs      Status = 0x0506
	StatusErrorBusy                  Status = 0x0507
	StatusErrorJobCanceled           Status = 0x0508
	StatusErrorMultipleDocuments     Status = 0x0509
)

var statusNames = map[Status]string{
	StatusOK:                         "successful-ok",
	StatusOKIgnoredOrSubstituted:     "successful-ok-ignored-or-substituted-attributes",
	StatusOKConflicting:              "successful-ok-conflicting-attributes",
	StatusErrorBadRequest:            "client-error-bad-request",
	StatusErrorForbidden:             "client-error-forbidden",
	StatusErrorNotAuthenticated:      "client-error-not-authenticated",
	StatusErrorNotAuthorized:         "client-error-not-authorized",
	StatusErrorNotPossible:           "client-error-not-possible",
	StatusErrorTimeout:               "client-error-timeout",
	StatusErrorNotFound:              "client-error-not-found",
	StatusErrorGone:                  "client-error-gone",
	StatusErrorRequestEntityTooLarge: "client-error-request-entity-too-large",
	StatusErrorRequestValueTooLong:   "client-error-request-value-too-long",
	StatusErrorDocumentFormat:        "client-error-document-format-not-supported",
	StatusErrorAttributesOrValues:    "client-error-attributes-or-values-not-supported",
	StatusErrorURIScheme:             "client-error-uri-scheme-not-supported",
	StatusErrorCharset:               "client-error-charset-not-supported",
	StatusErrorConflicting:           "client-error-conflicting-attributes",
	StatusErrorCompression:           "client-error-compression-not-supported",
	StatusErrorCompressionError:      "client-error-compression-error",
	StatusErrorDocumentFormatError:   "client-error-document-format-error",
	StatusErrorDocumentAccess:        "client-error-document-access-error",
	StatusErrorInternal:              "server-error-internal-error",
	StatusErrorOperationNotSupported: "server-error-operation-not-supported",
	StatusErrorServiceUnavailable:    "server-error-service-unavailable",
	StatusErrorVersionNotSupported:   "server-error-version-not-supported",
	StatusErrorDevice:                "server-error-device-error",
	StatusErrorTemporary:             "server-error-temporary-error",
	StatusErrorNotAcceptingJobs:      "server-error-not-accepting-jobs",
	StatusErrorBusy:                  "server-error-busy",
	StatusErrorJobCanceled:           "server-error-job-canceled",
	StatusErrorMultipleDocuments:     "server-error-multiple-document-jobs-not-supported",
}

// String returns the RFC 8011 keyword, e.g. "client-error-not-found".
func (s Status) String() string {
	if n, ok := statusNames[s]; ok {
		return n
	}
	return fmt.Sprintf("status(0x%04x)", uint16(s))
}

// IsError reports whether s is a client or server error (>= 0x0400).
func (s Status) IsError() bool { return s >= 0x0400 }

// StatusError is returned for responses with an error status code.
type StatusError struct {
	Code Status
	// Message is the status-message attribute of the response, if any.
	Message string
}

// Error returns the status keyword and message.
func (e *StatusError) Error() string {
	if e.Message == "" {
		return "ipp: " + e.Code.String()
	}
	return "ipp: " + e.Code.String() + ": " + e.Message
}

// PrinterState is the printer-state enum (RFC 8011 §5.4.11).
type PrinterState int

// Printer states.
const (
	PrinterIdle       PrinterState = 3
	PrinterProcessing PrinterState = 4
	PrinterStopped    PrinterState = 5
)

// String returns the keyword, e.g. "idle".
func (s PrinterState) String() string {
	switch s {
	case PrinterIdle:
		return "idle"
	case PrinterProcessing:
		return "processing"
	case PrinterStopped:
		return "stopped"
	}
	return fmt.Sprintf("printer-state(%d)", int(s))
}

// JobState is the job-state enum (RFC 8011 §5.3.7).
type JobState int

// Job states.
const (
	JobPending           JobState = 3
	JobPendingHeld       JobState = 4
	JobProcessing        JobState = 5
	JobProcessingStopped JobState = 6
	JobCanceled          JobState = 7
	JobAborted           JobState = 8
	JobCompleted         JobState = 9
)

// String returns the keyword, e.g. "processing-stopped".
func (s JobState) String() string {
	switch s {
	case JobPending:
		return "pending"
	case JobPendingHeld:
		return "pending-held"
	case JobProcessing:
		return "processing"
	case JobProcessingStopped:
		return "processing-stopped"
	case JobCanceled:
		return "canceled"
	case JobAborted:
		return "aborted"
	case JobCompleted:
		return "completed"
	}
	return fmt.Sprintf("job-state(%d)", int(s))
}

// Terminal reports whether s is a final state (canceled, aborted or
// completed).
func (s JobState) Terminal() bool {
	return s == JobCanceled || s == JobAborted || s == JobCompleted
}
