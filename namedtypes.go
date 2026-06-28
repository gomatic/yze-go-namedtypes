// Package namedtypes provides a go/analysis analyzer enforcing the gomatic Go
// standard that function parameters use named domain types rather than bare
// primitives. v1 covers non-method function declarations whose parameter type is
// a bare predeclared primitive identifier; methods and composite types are
// deferred.
package namedtypes

import (
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
	Group:      "go",
	Categories: []goyze.Category{"types"},
	URL:        "https://docs.gomatic.dev/yze/go/namedtypes",
	Analyzer:   Analyzer,
}

// run reports bare-primitive parameters of non-method function declarations.
func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		if fn := n.(*ast.FuncDecl); fn.Recv == nil {
			checkParams(pass, fn.Type.Params)
		}
	})
	return nil, nil
}

// checkParams reports each parameter whose type is a bare primitive.
func checkParams(pass *analysis.Pass, params *ast.FieldList) {
	for _, field := range params.List {
		if name, ok := barePrimitiveName(pass, field.Type); ok {
			pass.Reportf(field.Type.Pos(), "parameter type %s is a bare primitive; define a named domain type", name)
		}
	}
}

// barePrimitiveName returns the identifier name when expr is a bare predeclared
// primitive type, and false otherwise.
func barePrimitiveName(pass *analysis.Pass, expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	if _, ok := pass.TypesInfo.TypeOf(ident).(*types.Basic); !ok {
		return "", false
	}
	return ident.Name, true
}
