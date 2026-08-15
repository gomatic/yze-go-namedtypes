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
	if !fixEligible(declaringFile(pass, fn), fn, field) ||
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
	if !ok || isGeneric(named) || !types.Identical(named.Underlying(), prim) {
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

// declaringFile is the path fn's file was OPENED at, which is token.File.Name
// and never fset.Position — Position applies `//line` directives, ordinary
// compiled source that let the judged file decide its own identity in both
// directions: source carrying `//line zz_test.go:1` lost a fix it had earned,
// and a test file carrying `//line prod.go:1` collected one whose only promise
// is that it compiles against call sites this pass never loads.
func declaringFile(pass *analysis.Pass, fn *ast.FuncDecl) sourcePath {
	return sourcePath(pass.Fset.File(fn.Pos()).Name())
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

// isGeneric reports whether named is a parameterised type. `type N[T any] string`
// cannot be named bare in a parameter's type, so reusing it would retype the
// parameter to source that does not compile.
//
// The test is on type PARAMETERS, not type arguments. A package-scope lookup
// yields either a generic origin (parameters, no arguments) or an ordinary
// defined type; an instantiation reaches package scope only through an alias,
// which is rejected before this point, and `type M N[int]` is a fresh defined
// type with neither — legitimately reusable. Checking TypeArgs is therefore
// both insufficient (it passes the origin, which is the case that occurs) and
// unreachable.
func isGeneric(named *types.Named) bool {
	return named.TypeParams().Len() != 0
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
