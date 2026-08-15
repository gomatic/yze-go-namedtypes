//line zz_test.go:1
// Package linesrc pins the source half of a rule this analyzer applies to
// itself: fix eligibility turns on whether the declaration sits in a test file,
// and that answer must be read from the name the go tool opened the file at.
//
// The `//line` directive on the first line is a compiler feature for generated
// code. It changes what fset.Position reports and nothing else — the go tool
// still compiles this as ordinary source, `go list` still names it in GoFiles,
// and TestGoFiles stays empty. Deciding from the adjusted position lets one
// comment line withdraw the suggested fix from real production code.
package linesrc

// greet declares a bare primitive parameter and is fix-eligible in every
// respect: unexported, bodied, one non-variadic name, and every call site
// rewritable. Its diagnostic must carry a fix, because this is a source file
// whatever the directive above says.
func greet(name string) string { return "hi " + name } // want `parameter type string is a bare primitive; define a named domain type`

// Greet gives greet a rewritable call site, which is what makes the fix
// provable rather than withheld for an unrelated reason.
func Greet() string { return greet("x") }
