//line prod.go:1
// This file IS a test file — the go tool compiles it into the test binary and
// nowhere else — and it claims through a `//line` directive to be production
// source. The fix must still be withheld.
//
// The reason fixEligible excludes test files is that the production driver
// loads packages without them, so a rewrite proved against the production call
// sites is not proved against the test ones. Believing the adjusted name would
// let a test file collect a fix whose whole guarantee is that it compiles, and
// `--fix` would then apply it to bytes the analyzer never saw the callers of.
package linetest

// helper is fix-eligible in every respect except the one that matters: it is
// declared in a test file. Its diagnostic must stand without a fix.
func helper(name string) string { return "hi " + name } // want `parameter type string is a bare primitive; define a named domain type`

// useHelper gives helper the rewritable call site that would make the fix
// provable if this were production source, so the withheld fix is withheld for
// the file's identity and not for a missing call site.
func useHelper() string { return helper("x") }
