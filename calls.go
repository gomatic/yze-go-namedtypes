package namedtypes

// Call-site analysis: deciding whether every call of a function can be
// rewritten. This is the half of the fix that can say NO — a call it cannot
// account for means the whole rewrite is dropped, because a signature change
// applied to some call sites and not others does not compile.

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

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
