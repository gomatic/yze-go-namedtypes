// Package linetest holds the other direction: a real test file claiming,
// through a `//line` directive, to be production source. This file exists only
// so the package has production source of its own, and declares nothing the
// analyzer reports.
package linetest

// Anchor takes no parameters and is therefore never reported.
func Anchor() {}
