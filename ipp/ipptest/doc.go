// Package ipptest provides an in-process IPP mock server for tests.
//
// A [Server] serves a configurable set of printers over HTTP (and a unix
// socket where supported), records received jobs with their attributes and
// document data, and can inject error statuses, latency and an
// authentication requirement.
package ipptest
