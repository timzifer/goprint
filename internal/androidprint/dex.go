// Package androidprint holds the Java part of goprint's Android backend:
// PDFAdapter (java/), compiled to classes.dex by gen.sh.
package androidprint

import _ "embed"

// Dex is classes.dex with io.github.timzifer.goprint.PDFAdapter.
//
//go:embed classes.dex
var Dex []byte
