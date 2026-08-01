package namedtypes

import (
	"go/ast"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWrapEditsNeverNeedsTheSourceTextOfExpr names wrapEdits' claim. Wrapping by
// rewriting the expression would mean reproducing its source, and a printer
// round-trip is not byte-faithful: it normalises spacing, drops comments inside
// the expression, and re-parenthesises. Two zero-width insertions at the
// expression's boundaries leave every byte between them untouched.
func TestWrapEditsNeverNeedsTheSourceTextOfExpr(t *testing.T) {
	t.Parallel()
	_, _, file := checked(t, `package p
func caller(raw string) string { return domainArg(raw) }
func domainArg(s string) string { return s }
`)
	fn, _ := funcNamed(t, file, "caller")
	args := innerArgs(t, fn)
	require.Len(t, args, 1)
	expr := args[0]

	edits := wrapEdits(expr, "Domain")

	require.Len(t, edits, 2)
	assert.Equal(t, expr.Pos(), edits[0].Pos, "the opening insertion sits at the expression's start")
	assert.Equal(t, edits[0].Pos, edits[0].End, "and replaces nothing")
	assert.Equal(t, expr.End(), edits[1].Pos, "the closing insertion sits at the expression's end")
	assert.Equal(t, edits[1].Pos, edits[1].End, "and replaces nothing")
	assert.Equal(t, "Domain(", string(edits[0].NewText))
	assert.Equal(t, ")", string(edits[1].NewText))
}

// TestFixEditsProducesNonOverlappingEdits names the load-bearing half of
// fixEdits' "always compiles". go/analysis silently discards a SuggestedFix
// whose edits overlap, so an assembly that emits two edits over one byte range
// yields a diagnostic advertising a fix that does nothing at all — the failure
// mode a golden-file test cannot see, because the golden simply matches the
// unfixed source.
func TestFixEditsProducesNonOverlappingEdits(t *testing.T) {
	t.Parallel()
	_, info, file := checked(t, `package p
func withDir(dir string) string { return dir + "/" + dir }
func dirCaller() string { return withDir("root") }
`)
	fn, field := funcNamed(t, file, "withDir")
	caller, _ := funcNamed(t, file, "dirCaller")
	var args []ast.Expr
	ast.Inspect(caller.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			args = append(args, call.Args[0])
		}
		return true
	})
	require.Len(t, args, 1)

	edits := fixEdits(info, fn, field, "Dir", "string", args)

	require.NotEmpty(t, edits)
	assertDisjoint(t, edits)
}
