package namedtypes

// This file builds the reuse variant of the naming-oracle fix: when every
// in-pass call site of the flagged function already converts its argument from
// one existing same-package defined type N with underlying exactly the flagged
// primitive P (or passes a constant), the correct fix is to reuse N — retype
// the parameter to N, convert body uses back to P, drop the now-redundant
// call-site conversions, and wrap constant arguments in N. No new type is
// minted, so the per-pass minted-name dedupe does not apply: reuse fixes for
// the same N on different functions all carry fixes in one pass.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// reuseFix yields the reuse fix for the flagged parameter field of fn, or
// false when the reuse conditions do not hold: every call argument at the
// flagged position must be a conversion from one single same-package defined
// type N with underlying exactly the flagged primitive, or a constant; at
// least one conversion must exist; and N must resolve unshadowed at the
// parameter and at every constant argument the fix wraps in N.
func reuseFix(
	pass *analysis.Pass,
	fn *ast.FuncDecl,
	field *ast.Field,
	primitive identName,
	args []ast.Expr,
) ([]analysis.SuggestedFix, bool) {
	prim := pass.TypesInfo.ObjectOf(ast.Unparen(field.Type).(*ast.Ident)).Type()
	convs, consts, named, ok := classifyArguments(pass.TypesInfo, pass.Pkg, prim, args)
	if !ok || !visibleAtAll(pass.Pkg, named.Obj(), reusePositions(field, consts)) {
		return nil, false
	}
	return []analysis.SuggestedFix{{
		Message:   fmt.Sprintf("reuse the existing named type %s for this parameter", named.Obj().Name()),
		TextEdits: reuseEdits(pass.TypesInfo, fn, field, primitive, identName(named.Obj().Name()), convs, consts),
	}}, true
}

// classifyArguments splits args into conversions from one defined type N and
// constants, or false when any argument is neither, or two conversions
// disagree on N, or no conversion exists at all (nothing to reuse).
func classifyArguments(
	info *types.Info,
	pkg *types.Package,
	prim types.Type,
	args []ast.Expr,
) ([]*ast.CallExpr, []ast.Expr, *types.Named, bool) {
	var convs []*ast.CallExpr
	var consts []ast.Expr
	var named *types.Named
	for _, arg := range args {
		switch conv, argNamed, isConv := conversionFromNamed(info, arg, prim, pkg); {
		case isConv && (named == nil || types.Identical(argNamed, named)):
			named = argNamed
			convs = append(convs, conv)
		case !isConv && isConstant(info, arg):
			consts = append(consts, arg)
		default:
			return nil, nil, nil, false
		}
	}
	return convs, consts, named, named != nil
}

// conversionFromNamed reports whether arg is a conversion prim(x) where x's
// type is a non-generic defined (non-alias) type declared in pkg whose
// underlying type is exactly prim — the shape that proves callers already hold
// a domain type for this parameter.
func conversionFromNamed(
	info *types.Info,
	arg ast.Expr,
	prim types.Type,
	pkg *types.Package,
) (*ast.CallExpr, *types.Named, bool) {
	call, ok := arg.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, nil, false
	}
	callee := info.Types[call.Fun]
	if !callee.IsType() || !types.Identical(callee.Type, prim) {
		return nil, nil, false
	}
	named, ok := info.Types[call.Args[0]].Type.(*types.Named)
	if !ok || named.Obj().Pkg() != pkg || named.TypeArgs().Len() != 0 ||
		!types.Identical(named.Underlying(), prim) {
		return nil, nil, false
	}
	return call, named, true
}

// isConstant reports whether arg is a constant expression. Any constant an
// argument position accepts for the primitive parameter (an untyped constant,
// or a typed constant of the primitive) also converts to a defined type whose
// underlying type is that primitive, so wrapping it in N always compiles.
func isConstant(info *types.Info, arg ast.Expr) bool {
	return info.Types[arg].Value != nil
}

// reusePositions collects every position where the reuse fix writes a
// reference to N: the retyped parameter and each constant argument's wrap.
func reusePositions(field *ast.Field, consts []ast.Expr) []token.Pos {
	positions := make([]token.Pos, 0, 1+len(consts))
	positions = append(positions, field.Type.Pos())
	for _, arg := range consts {
		positions = append(positions, arg.Pos())
	}
	return positions
}

// visibleAtAll reports whether obj resolves unshadowed at every position.
func visibleAtAll(pkg *types.Package, obj types.Object, positions []token.Pos) bool {
	for _, pos := range positions {
		if !visibleAt(pkg, obj, pos) {
			return false
		}
	}
	return true
}

// visibleAt reports whether the name of obj, written at pos, resolves to obj's
// declaration — a same-package package-level type or a predeclared universe
// object, so it is visible unless a file import or an intervening declaration
// (a local one, or a parameter named after a predeclared type) shadows it.
func visibleAt(pkg *types.Package, obj types.Object, pos token.Pos) bool {
	scope := pkg.Scope().Innermost(pos)
	if scope == nil {
		return false
	}
	_, found := scope.LookupParent(obj.Name(), pos)
	return found == obj
}

// reuseEdits assembles the reuse fix: the parameter retype to N, a conversion
// back to the primitive around every body use (identical to the skeleton fix),
// the removal of each call-site conversion (leaving the already-typed operand
// as the argument), and a wrap of each constant argument in N. Each edit stays
// within its own argument's range, so two reuse fixes flagged on the same call
// expression never conflict.
func reuseEdits(
	info *types.Info,
	fn *ast.FuncDecl,
	field *ast.Field,
	primitive identName,
	name identName,
	convs []*ast.CallExpr,
	consts []ast.Expr,
) []analysis.TextEdit {
	uses := paramUses(info, fn.Body, info.Defs[field.Names[0]])
	edits := make([]analysis.TextEdit, 0, 1+2*(len(uses)+len(convs)+len(consts)))
	edits = append(edits, analysis.TextEdit{Pos: field.Type.Pos(), End: field.Type.End(), NewText: []byte(name)})
	for _, use := range uses {
		edits = append(edits, wrapEdits(use, primitive)...)
	}
	for _, conv := range convs {
		edits = append(edits, unwrapEdits(conv)...)
	}
	for _, arg := range consts {
		edits = append(edits, wrapEdits(arg, name)...)
	}
	return edits
}

// unwrapEdits deletes the conversion around conv's single operand — the text
// from the callee through the opening parenthesis, and the closing parenthesis
// — leaving the operand expression in place.
func unwrapEdits(conv *ast.CallExpr) []analysis.TextEdit {
	operand := conv.Args[0]
	return []analysis.TextEdit{
		{Pos: conv.Pos(), End: operand.Pos()},
		{Pos: operand.End(), End: conv.End()},
	}
}
