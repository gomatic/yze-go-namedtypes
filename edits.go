package namedtypes

// Edit assembly: turning a decided fix into byte ranges. Whether to rewrite is
// decided in fix.go and calls.go; these functions only express an already-made
// decision as edits go/analysis can apply. The halves fail differently — a
// wrong decision rewrites code it should not have touched, a wrong edit
// corrupts code it was right to touch.

import (
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// fixEdits assembles the fix: the skeleton type declaration above fn, the
// parameter retype, a conversion back to the primitive around every body use
// (conservative — it over-converts, but always compiles), and a wrap of every
// call-site argument in the new type.
func fixEdits(
	info *types.Info,
	fn *ast.FuncDecl,
	field *ast.Field,
	name identName,
	primitive identName,
	args []ast.Expr,
) []analysis.TextEdit {
	decl := declEdit(fn, identName(field.Names[0].Name), name, primitive)
	return append([]analysis.TextEdit{decl}, retypeEdits(info, fn, field, name, primitive, args)...)
}

// retypeEdits are the declaration-free edits shared by the minting and the
// minted-reuse fixes: retype the parameter, convert every body use back to
// the primitive, and wrap every in-package call-site argument in the type.
func retypeEdits(
	info *types.Info,
	fn *ast.FuncDecl,
	field *ast.Field,
	name identName,
	primitive identName,
	args []ast.Expr,
) []analysis.TextEdit {
	uses := paramUses(info, fn.Body, info.Defs[field.Names[0]])
	edits := make([]analysis.TextEdit, 0, 1+2*len(uses)+2*len(args))
	edits = append(edits,
		analysis.TextEdit{Pos: field.Type.Pos(), End: field.Type.End(), NewText: []byte(name)},
	)
	for _, use := range uses {
		edits = append(edits, wrapEdits(use, primitive)...)
	}
	for _, arg := range args {
		edits = append(edits, wrapEdits(arg, name)...)
	}
	return edits
}

// declEdit inserts the skeleton type declaration immediately above fn — before
// its doc comment when it has one — with a comment telling the developer to
// rename the type to the real domain concept.
func declEdit(fn *ast.FuncDecl, param, name, primitive identName) analysis.TextEdit {
	pos := fn.Pos()
	if fn.Doc != nil {
		pos = fn.Doc.Pos()
	}
	text := fmt.Sprintf(
		"// %s names the %s parameter of %s; rename it to the real domain concept.\ntype %s %s\n\n",
		name, param, fn.Name.Name, name, primitive,
	)
	return analysis.TextEdit{Pos: pos, End: pos, NewText: []byte(text)}
}

// wrapEdits wraps expr in a conversion to the named type or primitive `with`
// using two insertions, so the fix never needs the source text of expr.
func wrapEdits(expr ast.Expr, with identName) []analysis.TextEdit {
	return []analysis.TextEdit{
		{Pos: expr.Pos(), End: expr.Pos(), NewText: []byte(string(with) + "(")},
		{Pos: expr.End(), End: expr.End(), NewText: []byte(")")},
	}
}
