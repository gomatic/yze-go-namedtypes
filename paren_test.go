package namedtypes_test

// Corpus for parenthesized parameter types: func f(a (int)) and
// func f(a ...(int)) are valid Go that must not escape the analyzer, but gofmt
// (and so the repo's formatting gate) rewrites the parentheses away, so a
// static testdata file cannot carry these shapes. The corpus is materialized
// into a temporary GOPATH-shaped tree at test time instead; the analyzer and
// its suggested fixes run against it exactly as against the static corpora.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"

	namedtypes "github.com/gomatic/yze-go-namedtypes"
)

// parenSource is the corpus input. withParen and parenTyped are fix-eligible,
// so the fixes must see through the parentheses too: the retype edit replaces
// the whole parenthesized type.
const parenSource = `package paren

// withParen parenthesizes a bare primitive; the parentheses are seen through
// and the parameter is still flagged.
func withParen(a (int)) {} // want "parameter type int is a bare primitive; define a named domain type"

// variadicParen parenthesizes the variadic element type; the parentheses are
// seen through and the element type is still flagged (variadic, so no fix).
func variadicParen(nums ...(int)) {} // want "parameter type int is a bare primitive; define a named domain type"

// parenTyped is fix-eligible: the whole parenthesized type is retyped and the
// body use is converted back to the primitive.
func parenTyped(pixels (int)) int { // want "parameter type int is a bare primitive; define a named domain type"
	return pixels * 3
}
`

// parenGolden is the corpus after applying the suggested fixes; analysistest
// gofmt-normalizes both sides, so the remaining parentheses compare equal.
const parenGolden = `package paren

// aParam names the a parameter of withParen; rename it to the real domain concept.
type aParam int

// withParen parenthesizes a bare primitive; the parentheses are seen through
// and the parameter is still flagged.
func withParen(a aParam) {} // want "parameter type int is a bare primitive; define a named domain type"

// variadicParen parenthesizes the variadic element type; the parentheses are
// seen through and the element type is still flagged (variadic, so no fix).
func variadicParen(nums ...(int)) {} // want "parameter type int is a bare primitive; define a named domain type"

// pixelsParam names the pixels parameter of parenTyped; rename it to the real domain concept.
type pixelsParam int

// parenTyped is fix-eligible: the whole parenthesized type is retyped and the
// body use is converted back to the primitive.
func parenTyped(pixels pixelsParam) int { // want "parameter type int is a bare primitive; define a named domain type"
	return int(pixels) * 3
}
`

func TestParenthesizedTypesAreSeenThrough(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "src", "paren")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "paren.go"), []byte(parenSource), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "paren.go.golden"), []byte(parenGolden), 0o644))
	analysistest.RunWithSuggestedFixes(t, dir, namedtypes.Analyzer, "paren")
}
