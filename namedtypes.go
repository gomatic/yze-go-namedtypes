// Package namedtypes provides a go/analysis analyzer enforcing the gomatic Go
// standard that function parameters use named domain types rather than bare
// primitives. v1 covers non-method function declarations whose parameter type is
// a bare predeclared primitive identifier; methods and composite types are
// deferred.
package namedtypes

import (
	"fmt"
	"go/ast"
	"go/types"

	goyze "github.com/gomatic/go-yze"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer reports function parameters declared with a bare primitive type.
var Analyzer = &analysis.Analyzer{
	Name:     "namedtypes",
	Doc:      "reports function parameters that use a bare primitive type instead of a named domain type",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// Registration declares this analyzer to the yze framework.
var Registration = goyze.Registration{
	Name:       "namedtypes",
	Categories: []goyze.Category{"types"},
	URL:        "https://docs.gomatic.dev/yze/namedtypes",
	Analyzer:   Analyzer,
}

// run reports bare-primitive parameters of non-method function declarations.
// The fixed set deduplicates proposed skeleton type names across the pass, so
// only the first (by position) of several diagnostics minting the same name
// carries the fix.
func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	fixed := map[identName]bool{}
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		if fn := n.(*ast.FuncDecl); fn.Recv == nil {
			checkParams(pass, fn, fixed)
		}
	})
	return nil, nil
}

// checkParams reports each parameter whose type is a bare primitive, attaching
// the naming-oracle suggested fix when one can be built safely (see fix.go). A
// parameter whose every name is the blank identifier is exempt: it exists only
// to satisfy a signature the package does not control (an interface method, a
// framework contract), the function cannot use its value, and no domain
// concept exists to name.
func checkParams(pass *analysis.Pass, fn *ast.FuncDecl, fixed map[identName]bool) {
	for _, field := range fn.Type.Params.List {
		if allBlank(field.Names) {
			continue
		}
		typ := paramType(field.Type)
		name, ok := barePrimitiveName(pass, typ)
		if !ok {
			continue
		}
		pass.Report(analysis.Diagnostic{
			Pos:            typ.Pos(),
			Message:        fmt.Sprintf("parameter type %s is a bare primitive; define a named domain type", name),
			SuggestedFixes: suggestedFixes(pass, fn, field, name, fixed),
		})
	}
}

// allBlank reports whether the field declares names and every one is the blank
// identifier. An unnamed field (no names at all) is not exempt: it still shapes
// the function's public signature.
func allBlank(names []*ast.Ident) bool {
	if len(names) == 0 {
		return false
	}
	for _, name := range names {
		if name.Name != "_" {
			return false
		}
	}
	return true
}

// paramType yields the element type of a variadic parameter (the type after the
// `...`), or the field's type unchanged otherwise, so a variadic bare primitive
// (func f(nums ...int)) is checked and reported at its element type rather than
// being skipped because its field type is an *ast.Ellipsis. Parentheses are
// seen through, so func f(nums ...(int)) is checked at its element type too.
func paramType(expr ast.Expr) ast.Expr {
	if ellipsis, ok := ast.Unparen(expr).(*ast.Ellipsis); ok {
		return ellipsis.Elt
	}
	return expr
}

// barePrimitiveName returns the identifier name when expr names a predeclared
// primitive type by its predeclared name, and false otherwise. Parentheses are
// seen through, so func f(a (int)) does not escape the check. The exemption is
// resolved through the identifier's declaring object rather than the type it
// presents, so it holds regardless of the gotypesalias GODEBUG setting (under
// which an alias to a primitive may otherwise surface as a bare *types.Basic):
//   - a named domain type — a defined type (type Count int) or an alias (type
//     Celsius = float64) — is declared in a package (Pkg() != nil), so exempt;
//   - a predeclared object lives in the universe scope (Pkg() == nil); only one
//     whose underlying type is a primitive (excludes the predeclared error/any
//     interfaces, while still catching the byte/rune primitive aliases) is flagged.
func barePrimitiveName(pass *analysis.Pass, expr ast.Expr) (identName, bool) {
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	if !ok {
		return "", false
	}
	obj := pass.TypesInfo.ObjectOf(ident)
	if obj.Pkg() != nil {
		return "", false
	}
	if _, ok := obj.Type().Underlying().(*types.Basic); !ok {
		return "", false
	}
	return identName(ident.Name), true
}
