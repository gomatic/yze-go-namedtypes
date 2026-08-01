package namedtypes_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"

	namedtypes "github.com/gomatic/yze-go-namedtypes"
)

func TestBarePrimitiveParameterIsReported(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), namedtypes.Analyzer, "a")
}

func TestSuggestedFixesIntroduceSkeletonTypes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), namedtypes.Analyzer, "fix")
}

func TestRegistrationIsWellFormed(t *testing.T) {
	assert.NoError(t, namedtypes.Registration.Validate())
	assert.Equal(t, "yze/namedtypes", namedtypes.Registration.RuleID())
	assert.Same(t, namedtypes.Analyzer, namedtypes.Registration.Analyzer)
}

// diagnosticsIn runs the analyzer over a corpus package and groups its
// diagnostics by the function they were reported inside. The corpus's `// want`
// comments already assert WHICH functions are flagged; these tests assert the
// orthogonal property — which diagnostics carry a fix, and which deliberately
// do not. A diagnostic without a fix is the analyzer saying "I see the problem
// but rewriting it is not provably safe", and that restraint is the contract.
func diagnosticsIn(t *testing.T, pkg string) map[string][]analysis.Diagnostic {
	t.Helper()
	results := analysistest.Run(t, analysistest.TestData(), namedtypes.Analyzer, pkg)
	require.Len(t, results, 1)

	byFunc := map[string][]analysis.Diagnostic{}
	for _, diag := range results[0].Diagnostics {
		byFunc[enclosingFunc(results[0].Pass, diag)] = append(byFunc[enclosingFunc(results[0].Pass, diag)], diag)
	}
	return byFunc
}

// enclosingFunc names the function a diagnostic was reported inside.
func enclosingFunc(pass *analysis.Pass, diag analysis.Diagnostic) string {
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Pos() <= diag.Pos && diag.Pos < fn.End() {
				return fn.Name.Name
			}
		}
	}
	return ""
}

// TestWrappableArgumentsRefusesUnprovableCallSites names wrappableArguments'
// claim, which is stated as a guarantee about compilation. Each function below
// is a way that guarantee fails: the function is referenced as a value so its
// signature change escapes the calls being rewritten, a call spreads a
// multi-value result so the flagged argument is not a syntactic argument, or
// the function calls itself so the argument wrap and the body conversions would
// collide on the same bytes. All must still be REPORTED and carry no fix.
func TestWrappableArgumentsRefusesUnprovableCallSites(t *testing.T) {
	byFunc := diagnosticsIn(t, "fix")

	for _, fn := range []string{"valueUsed", "pairArgs", "recursive"} {
		diags := byFunc[fn]
		require.NotEmpty(t, diags, "%s must still be reported", fn)
		for _, diag := range diags {
			assert.Empty(t, diag.SuggestedFixes,
				"%s cannot be proved rewritable, so its diagnostic must stand without a fix", fn)
		}
	}
}

// TestRunDeduplicatesMintedNamesAcrossThePass names run's claim. Two parameters
// sharing a name would mint the same package-level type, and applying both
// fixes in one pass writes that declaration twice — source that does not
// compile. Only the first by position may carry the fix; --fix users reach the
// rest by iterating to a fixpoint.
func TestRunDeduplicatesMintedNamesAcrossThePass(t *testing.T) {
	byFunc := diagnosticsIn(t, "fix")

	first, second := byFunc["firstDup"], byFunc["secondDup"]
	require.Len(t, first, 1)
	require.Len(t, second, 1)

	assert.NotEmpty(t, first[0].SuggestedFixes, "the first by position carries the fix")
	assert.Empty(t, second[0].SuggestedFixes, "the second would mint the same name a second time")
}

// TestCheckParamsExemptsTheBlankIdentifier names checkParams' claim. A wholly
// blank parameter exists to satisfy a signature the package does not control,
// the body cannot read it, and no domain concept exists to name — so reporting
// it would be noise the author cannot act on without changing someone else's
// signature.
func TestCheckParamsExemptsTheBlankIdentifier(t *testing.T) {
	byFunc := diagnosticsIn(t, "a")

	assert.Empty(t, byFunc["blankParam"],
		"a parameter named only _ carries no domain concept and must not be reported")
	assert.NotEmpty(t, byFunc, "the corpus must still report something, or the exemption proves nothing")
}

// TestReuseFixAdoptsAnExistingDomainTypeOverMintingASkeleton names reuseFix's
// claim. When every call already converts from one same-package defined type,
// that type IS the domain concept the analyzer would otherwise ask the author
// to invent; minting a second name would split one concept across two types.
func TestReuseFixAdoptsAnExistingDomainTypeOverMintingASkeleton(t *testing.T) {
	byFunc := diagnosticsIn(t, "fix")

	diags := byFunc["writeRoot"]
	require.Len(t, diags, 1)
	require.NotEmpty(t, diags[0].SuggestedFixes, "an existing domain type makes the rewrite provable")

	var wrote string
	for _, edit := range diags[0].SuggestedFixes[0].TextEdits {
		wrote += string(edit.NewText)
	}
	assert.Contains(t, wrote, "FilePath", "the fix adopts the type callers already hold")
	assert.NotContains(t, wrote, "type Name", "and mints no second name for the same concept")
}

// Corpus for parenthesized parameter types: func f(a (int)) and
// func f(a ...(int)) are valid Go that must not escape the analyzer, but gofmt
// (and so the repo's formatting gate) rewrites the parentheses away, so a
// static testdata file cannot carry these shapes. The corpus is materialized
// into a temporary GOPATH-shaped tree at test time instead; the analyzer and
// its suggested fixes run against it exactly as against the static corpora.
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
