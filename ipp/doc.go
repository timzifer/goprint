// Package ipp implements the Internet Printing Protocol (RFC 8010/8011):
// an attribute codec and a client for CUPS and IPP Everywhere printers.
//
// The package is usable on its own and has no platform dependencies.
//
// # Codec
//
// A [Message] is a request or response: version, operation-id or
// status-code, request-id and attribute groups. Attribute values are typed
// ([Integer], [Keyword], [Collection], [OutOfBand], ...), and every value
// carries its own tag, so multi-valued attributes may mix syntaxes.
// [Message.MarshalBinary] and [Decode] convert between messages and the
// wire format; decoding then re-encoding is lossless for every message the
// decoder accepts. The decoder never panics on malformed input, bounds its
// allocations by the input size (or [MaxMessageSize] for streams) and
// limits collection nesting to [MaxCollectionDepth].
//
// # Client
//
// A [Client] posts requests over HTTP/1.1 to ipp://, ipps://, http(s)://
// servers or a unix socket; [NewCUPSClient] finds the local CUPS scheduler.
// Typed helpers cover CUPS-Get-Printers, CUPS-Get-Default,
// Get-Printer-Attributes, Print-Job, Get-Job-Attributes and Cancel-Job and
// fill in the mandatory operation attributes. Error statuses are returned
// as *[StatusError]; successful statuses that report ignored or substituted
// attributes are not errors but expose them via [Job.Unsupported] and
// [Message.Unsupported].
//
// Package [github.com/timzifer/goprint/ipp/ipptest] provides an in-process
// server for tests.
package ipp
