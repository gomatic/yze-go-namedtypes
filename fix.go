package namedtypes

// This file builds the "naming oracle" suggested fix for an eligible bare-
// primitive parameter. It first attempts to reuse an existing same-package
// named type when every in-pass call site already converts from that type
// (see reuse.go); otherwise it mechanically introduces a named skeleton type
// derived from the parameter name (<param>Param), retypes the parameter,
// converts every body use back to the primitive, and wraps every in-package
// call-site argument in the new type. The skeleton is deliberately a rename-me
// placeholder, not the final domain name; either fix must always compile
// within the files the pass can see.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// identName names an identifier the fix mints or inspects: a proposed type
// name, a predeclared primitive, or a parameter name.
type identName string

// sourcePath is a file path as reported by the pass's FileSet.
type sourcePath string

// flatIndex is a zero-based position in a flattened parameter list, counting
// every name of every field.
type flatIndex int

// flatLen is the flattened length of a parameter list.
type flatLen int

// typeNameSuffix marks the minted type as a mechanical skeleton: <param>Param
// is unexported, grammatically neutral, and obviously awaiting a real name.
const typeNameSuffix identName = "Param"

// suggestedFixes yields the single naming-oracle fix for the flagged parameter
// field of fn, or nil when no fix can be guaranteed to compile within the
// pass: the function or parameter shape is ineligible, the parameter is
// mutated or addressed, the primitive's name is shadowed at a body use the fix
// would wrap, or a call site cannot be safely rewritten. When every call site
// already converts from one existing same-package named type, the fix reuses
// that type (see reuse.go); otherwise it mints a skeleton type, skipped when
// the proposed name is taken (or already minted by an earlier diagnostic in
// this pass — `--fix` users iterate to fixpoint).
func suggestedFixes(
	pass *analysis.Pass,
	fn *ast.FuncDecl,
	field *ast.Field,
	primitive identName,
	fixed map[identName]bool,
) []analysis.SuggestedFix {
	if !fixEligible(sourcePath(pass.Fset.Position(fn.Pos()).Filename), fn, field) ||
		unsafeUse(pass.TypesInfo, fn.Body, pass.TypesInfo.Defs[field.Names[0]]) ||
		!primitiveVisible(pass, fn, field, primitive) {
		return nil
	}
	args, ok := wrappableArguments(pass, fn, paramIndex(fn.Type.Params, field), paramCount(fn.Type.Params))
	if !ok {
		return nil
	}
	if fix, ok := reuseFix(pass, fn, field, primitive, args); ok {
		return fix
	}
	return skeletonFix(pass, fn, field, primitive, fixed, args)
}

// skeletonFix yields the minting fix — a fresh <param>Param skeleton type —
// or nil when the proposed name is already declared in the package or already
// minted by an earlier diagnostic in this pass.
func skeletonFix(
	pass *analysis.Pass,
	fn *ast.FuncDecl,
	field *ast.Field,
	primitive identName,
	fixed map[identName]bool,
	args []ast.Expr,
) []analysis.SuggestedFix {
	name := identName(field.Names[0].Name) + typeNameSuffix
	if fixed[name] {
		return nil
	}
	prim := pass.TypesInfo.ObjectOf(ast.Unparen(field.Type).(*ast.Ident)).Type()
	if named, ok := mintedType(pass.Pkg, name, prim); ok {
		return mintedReuseFix(pass.Pkg, pass.TypesInfo, fn, field, primitive, named, args)
	}
	if nameTaken(pass.TypesInfo, name) {
		return nil
	}
	fixed[name] = true
	return []analysis.SuggestedFix{{
		Message:   fmt.Sprintf("introduce named type %s for parameter %s", name, field.Names[0].Name),
		TextEdits: fixEdits(pass.TypesInfo, fn, field, name, primitive, args),
	}}
}

// mintedType reports the package-level defined type called name whose
// underlying is exactly prim — typically the skeleton a previous --fix round
// minted for a same-named parameter elsewhere in the package. A name held by
// anything else (an alias, a generic type, a different underlying, a non-type)
// does not qualify.
func mintedType(pkg *types.Package, name identName, prim types.Type) (*types.Named, bool) {
	obj, ok := pkg.Scope().Lookup(string(name)).(*types.TypeName)
	if !ok || obj.IsAlias() {
		return nil, false
	}
	named, ok := obj.Type().(*types.Named)
	if !ok || named.TypeArgs().Len() != 0 || !types.Identical(named.Underlying(), prim) {
		return nil, false
	}
	return named, true
}

// mintedReuseFix retypes the parameter to the already-minted skeleton type:
// the same edits as minting minus the declaration. The name is written at the
// parameter and at every call argument, so it must resolve unshadowed at each
// of those positions; the fixpoint loop reaches this path on the round after
// the first same-named parameter minted the type.
func mintedReuseFix(
	pkg *types.Package,
	info *types.Info,
	fn *ast.FuncDecl,
	field *ast.Field,
	primitive identName,
	named *types.Named,
	args []ast.Expr,
) []analysis.SuggestedFix {
	if !visibleAtAll(pkg, named.Obj(), reusePositions(field, args)) {
		return nil
	}
	name := identName(named.Obj().Name())
	return []analysis.SuggestedFix{{
		Message:   fmt.Sprintf("reuse the existing named type %s for this parameter", name),
		TextEdits: retypeEdits(info, fn, field, name, primitive, args),
	}}
}

// fixEligible reports whether the flagged parameter may be retyped at all: the
// function is unexported (exported functions have out-of-package callers the
// pass cannot rewrite), declared in a non-test file (the production driver
// loads packages without test files), has a body to rewrite, and the field
// declares exactly one name (a shared `a, b string` field is skipped for
// simplicity) that is not variadic (slice conversions do not exist).
func fixEligible(path sourcePath, fn *ast.FuncDecl, field *ast.Field) bool {
	return !fn.Name.IsExported() &&
		!strings.HasSuffix(string(path), "_test.go") &&
		fn.Body != nil &&
		len(field.Names) == 1 &&
		!isVariadic(field)
}

// isVariadic reports whether the field is a variadic parameter.
func isVariadic(field *ast.Field) bool {
	_, ok := field.Type.(*ast.Ellipsis)
	return ok
}

// unsafeUse reports whether obj is used inside body in a way that retyping the
// parameter cannot survive: written to (assignment LHS, increment/decrement,
// or a `=`-form range clause) or having its address taken (a conversion is not
// addressable, and the resulting pointer type would leak the new type).
func unsafeUse(info *types.Info, body *ast.BlockStmt, obj types.Object) bool {
	unsafe := false
	ast.Inspect(body, func(n ast.Node) bool {
		unsafe = unsafe || nodeMutates(info, n, obj)
		return !unsafe
	})
	return unsafe
}

// nodeMutates reports whether n writes to obj or takes its address.
func nodeMutates(info *types.Info, n ast.Node, obj types.Object) bool {
	switch node := n.(type) {
	case *ast.AssignStmt:
		return anyIdentIs(info, node.Lhs, obj)
	case *ast.IncDecStmt:
		return identIs(info, node.X, obj)
	case *ast.RangeStmt:
		return node.Tok == token.ASSIGN &&
			(identIs(info, node.Key, obj) || identIs(info, node.Value, obj))
	case *ast.UnaryExpr:
		return node.Op == token.AND && identIs(info, node.X, obj)
	}
	return false
}

// identIs reports whether expr is an identifier resolving to obj.
func identIs(info *types.Info, expr ast.Expr, obj types.Object) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && info.ObjectOf(ident) == obj
}

// anyIdentIs reports whether any expression in exprs is an identifier
// resolving to obj.
func anyIdentIs(info *types.Info, exprs []ast.Expr, obj types.Object) bool {
	for _, expr := range exprs {
		if identIs(info, expr, obj) {
			return true
		}
	}
	return false
}

// nameTaken reports whether name is already declared by any identifier in the
// package's files. This is deliberately more conservative than a scope walk: a
// hit in any scope — package, file, or local — skips the fix rather than risk
// a shadowed reference at a rewritten call site.
func nameTaken(info *types.Info, name identName) bool {
	for _, obj := range info.Defs {
		if obj != nil && obj.Name() == string(name) {
			return true
		}
	}
	return false
}

// paramIndex yields target's zero-based position in the flattened parameter
// list, counting every name of every preceding field.
func paramIndex(params *ast.FieldList, target *ast.Field) flatIndex {
	index := flatIndex(0)
	for _, field := range params.List {
		if field == target {
			break
		}
		index += flatIndex(len(field.Names))
	}
	return index
}

// paramCount yields the flattened length of the parameter list.
func paramCount(params *ast.FieldList) flatLen {
	count := flatLen(0)
	for _, field := range params.List {
		count += flatLen(len(field.Names))
	}
	return count
}

// wrappableArguments yields the argument expression at index of every call to
// fn within the pass, or false when a rewrite cannot be guaranteed to compile:
// fn is referenced as a value somewhere (its signature change would propagate
// beyond the calls the fix rewrites), a call does not pass exactly count
// single-valued arguments (a spread `f(g())` call cannot be wrapped), or fn
// calls itself (the argument wrap would collide with the same fix's body-use
// conversions).
func wrappableArguments(
	pass *analysis.Pass,
	fn *ast.FuncDecl,
	index flatIndex,
	count flatLen,
) ([]ast.Expr, bool) {
	obj := pass.TypesInfo.Defs[fn.Name]
	calls := functionCalls(pass.TypesInfo, pass.Files, obj)
	if countUses(pass.TypesInfo, pass.Files, obj) != len(calls) {
		return nil, false
	}
	args, ok := callArguments(calls, index, count)
	if !ok || selfCall(fn, args) {
		return nil, false
	}
	return args, true
}

// functionCalls collects every call expression in files whose callee is obj.
func functionCalls(info *types.Info, files []*ast.File, obj types.Object) []*ast.CallExpr {
	var calls []*ast.CallExpr
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && identIs(info, ast.Unparen(call.Fun), obj) {
				calls = append(calls, call)
			}
			return true
		})
	}
	return calls
}

// countUses counts every identifier in files that uses obj, so a caller can
// detect uses that are not direct calls.
func countUses(info *types.Info, files []*ast.File, obj types.Object) int {
	count := 0
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok && info.Uses[ident] == obj {
				count++
			}
			return true
		})
	}
	return count
}

// callArguments yields the argument at index from each call, or false when a
// call does not pass exactly count arguments (a spread multi-value call, or a
// variadic call with a different argument count).
func callArguments(calls []*ast.CallExpr, index flatIndex, count flatLen) ([]ast.Expr, bool) {
	args := make([]ast.Expr, 0, len(calls))
	for _, call := range calls {
		if flatLen(len(call.Args)) != count {
			return nil, false
		}
		args = append(args, call.Args[int(index)])
	}
	return args, true
}

// selfCall reports whether any collected argument lies inside fn's own body —
// a recursive call, whose argument wrap would occupy the same positions as the
// body-use conversions of the same fix.
func selfCall(fn *ast.FuncDecl, args []ast.Expr) bool {
	for _, arg := range args {
		if fn.Body.Pos() <= arg.Pos() && arg.Pos() < fn.Body.End() {
			return true
		}
	}
	return false
}

// paramUses collects every identifier in body that uses obj. The fix wraps
// each in a conversion back to the primitive; assignment targets never appear
// here because a mutated parameter is rejected by unsafeUse, and the declaring
// identifier is a definition, not a use.
func paramUses(info *types.Info, body *ast.BlockStmt, obj types.Object) []*ast.Ident {
	var uses []*ast.Ident
	ast.Inspect(body, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && info.Uses[ident] == obj {
			uses = append(uses, ident)
		}
		return true
	})
	return uses
}

// primitiveVisible reports whether the primitive's name resolves to its
// predeclared universe object at every body use of the parameter — every
// position the fix wraps in a conversion back to the primitive. A parameter
// named after the primitive, or a local declaration reusing its name, shadows
// it there; the wrap would not resolve to the predeclared type (so it would
// not compile), and the fix is suppressed entirely.
func primitiveVisible(pass *analysis.Pass, fn *ast.FuncDecl, field *ast.Field, primitive identName) bool {
	uses := paramUses(pass.TypesInfo, fn.Body, pass.TypesInfo.Defs[field.Names[0]])
	positions := make([]token.Pos, 0, len(uses))
	for _, use := range uses {
		positions = append(positions, use.Pos())
	}
	return visibleAtAll(pass.Pkg, types.Universe.Lookup(string(primitive)), positions)
}

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
