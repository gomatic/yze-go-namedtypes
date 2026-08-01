package namedtypes

// White-box test for the visibility contract on a shape the analysistest
// corpus cannot express: every position the corpus produces lies inside one of
// the pass's file scopes, so the no-enclosing-scope guard is reachable only
// through the API. The contract it pins: a position outside every file scope
// resolves nothing, so the reuse fix is skipped rather than emitted with a
// reference that cannot be checked.

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
)

func TestVisibleAtOutsideAnyFileScope(t *testing.T) {
	pkg := types.NewPackage("example.test/p", "p")
	named := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "N", nil), types.Typ[types.String], nil)
	assert.False(t, visibleAt(pkg, named.Obj(), token.Pos(42)))
}

// checked type-checks src as package p and returns everything the helpers under
// test need. A real type-checked package is required rather than a synthetic
// AST: the claims below are about TYPES — underlying identity, aliasing,
// generic instantiation, constant-ness — none of which a hand-built ast.Ident
// carries.
func checked(t *testing.T, src string) (*types.Package, *types.Info, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)

	info := &types.Info{
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
		Types: map[ast.Expr]types.TypeAndValue{},
	}
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check("example.test/p", fset, []*ast.File{file}, info)
	require.NoError(t, err)
	return pkg, info, file
}

// funcNamed returns the declaration of fn and its first parameter field, or a
// nil field when the function takes no parameters.
func funcNamed(t *testing.T, file *ast.File, name string) (*ast.FuncDecl, *ast.Field) {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name {
			continue
		}
		if len(fn.Type.Params.List) == 0 {
			return fn, nil
		}
		return fn, fn.Type.Params.List[0]
	}
	t.Fatalf("no func %s in the fixture", name)
	return nil, nil
}

// innerArgs returns the single argument of each domainArg call in fn's body.
func innerArgs(t *testing.T, fn *ast.FuncDecl) []ast.Expr {
	t.Helper()
	var args []ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok && len(call.Args) == 1 {
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == "domainArg" {
				args = append(args, call.Args[0])
			}
		}
		return true
	})
	return args
}

// TestConversionFromNamedRequiresADefinedSamePackageType names
// conversionFromNamed's claim. It is the evidence the reuse fix rests on —
// "callers already hold a domain type" — so each rejected shape is a case where
// that evidence is absent and adopting a type would be a guess. An alias is the
// primitive itself, so adopting it would emit a fix that changes nothing; a
// different underlying would not compile.
func TestConversionFromNamedRequiresADefinedSamePackageType(t *testing.T) {
	t.Parallel()
	pkg, info, file := checked(t, `package p
type Domain string
type Aliased = string
type Wrong int
type Generic[T any] string
func caller(raw string) {
	_ = domainArg(string(Domain(raw)))
	_ = domainArg(string(Aliased(raw)))
	_ = domainArg(string(Wrong(1)))
	_ = domainArg(string(Generic[int](raw)))
	_ = domainArg(raw)
}
func domainArg(s string) string { return s }
`)
	fn, _ := funcNamed(t, file, "caller")
	args := innerArgs(t, fn)
	require.Len(t, args, 5)
	str := types.Typ[types.String]

	_, named, ok := conversionFromNamed(info, args[0], str, pkg)
	require.True(t, ok, "a conversion from a same-package defined type over string qualifies")
	assert.Equal(t, "Domain", named.Obj().Name())

	for i, why := range []string{
		"an alias is the primitive itself",
		"a different underlying would not compile",
		"a generic instantiation is not a plain defined type",
		"a bare identifier is no conversion at all",
	} {
		_, _, ok := conversionFromNamed(info, args[i+1], str, pkg)
		assert.False(t, ok, why)
	}
}

// TestIsConstantAcceptsOnlyConstantExpressions names isConstant's claim that
// wrapping the argument in N "always compiles". That holds exactly for
// expressions with a constant VALUE, which convert implicitly to any type over
// the right underlying. A variable does not, so accepting one would emit N(x)
// where x already has a conflicting type.
func TestIsConstantAcceptsOnlyConstantExpressions(t *testing.T) {
	t.Parallel()
	_, info, file := checked(t, `package p
const typed string = "t"
func caller(raw string) {
	_ = domainArg("literal")
	_ = domainArg(typed)
	_ = domainArg("a" + "b")
	_ = domainArg(raw)
}
func domainArg(s string) string { return s }
`)
	fn, _ := funcNamed(t, file, "caller")
	args := innerArgs(t, fn)
	require.Len(t, args, 4)

	for i, why := range []string{"an untyped literal", "a typed constant", "a folded constant expression"} {
		assert.True(t, isConstant(info, args[i]), why+" is constant")
	}
	assert.False(t, isConstant(info, args[3]), "a variable is not constant and cannot be wrapped")
}

// TestReusePositionsCoversEveryReferenceTheFixWrites names reusePositions'
// claim. Every position it returns is checked for N's visibility before the fix
// is emitted, so a position it omits is a reference written into scope nobody
// verified — source that does not compile, or worse resolves to a shadowing
// declaration and compiles into different behaviour.
func TestReusePositionsCoversEveryReferenceTheFixWrites(t *testing.T) {
	t.Parallel()
	_, _, file := checked(t, `package p
func caller(raw string) {
	_ = domainArg("one")
	_ = domainArg("two")
}
func domainArg(s string) string { return s }
`)
	fn, _ := funcNamed(t, file, "caller")
	consts := innerArgs(t, fn)
	require.Len(t, consts, 2)
	_, field := funcNamed(t, file, "domainArg")

	positions := reusePositions(field, consts)

	require.Len(t, positions, 3, "the retyped parameter plus each wrapped constant")
	assert.Equal(t, field.Type.Pos(), positions[0], "the parameter's type is where N is first written")
	assert.Equal(t, consts[0].Pos(), positions[1])
	assert.Equal(t, consts[1].Pos(), positions[2])
	assert.Len(t, reusePositions(field, nil), 1, "with no constants only the parameter is written")
}

// TestReuseEditsStayWithinEachArgumentsRange names reuseEdits' claim that two
// reuse fixes flagged on the same call expression never conflict. They are
// separate diagnostics, so the user may apply both; go/analysis silently drops
// a fix whose edits overlap another's, which would turn "apply all" into
// "apply some" with no report. Every edit must therefore sit inside the
// parameter or the argument it belongs to.
func TestReuseEditsStayWithinEachArgumentsRange(t *testing.T) {
	t.Parallel()
	_, info, file := checked(t, `package p
type Domain string
func domainArg(s string) string { return s + s }
func caller(d Domain) string { return domainArg(string(d)) }
`)
	fn, field := funcNamed(t, file, "domainArg")
	caller, _ := funcNamed(t, file, "caller")
	var convs []*ast.CallExpr
	ast.Inspect(caller.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && len(call.Args) == 1 {
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == "string" {
				convs = append(convs, call)
			}
		}
		return true
	})
	require.Len(t, convs, 1)

	edits := reuseEdits(info, fn, field, "string", "Domain", convs, nil)

	require.NotEmpty(t, edits)
	assertDisjoint(t, edits)
	for _, edit := range edits {
		inParam := edit.Pos >= fn.Type.Params.Pos() && edit.End <= fn.Type.Params.End()
		inBody := edit.Pos >= fn.Body.Pos() && edit.End <= fn.Body.End()
		inArg := edit.Pos >= convs[0].Pos() && edit.End <= convs[0].End()
		assert.True(t, inParam || inBody || inArg,
			"edit at %v..%v escapes the parameter, the body and its own argument", edit.Pos, edit.End)
	}
}

// assertDisjoint fails when two edits share a byte, which is what makes
// go/analysis drop the fix.
func assertDisjoint(t *testing.T, edits []analysis.TextEdit) {
	t.Helper()
	for i := range edits {
		for j := i + 1; j < len(edits); j++ {
			a, b := edits[i], edits[j]
			assert.False(t, a.Pos < b.End && b.Pos < a.End,
				"edits %d (%v..%v) and %d (%v..%v) overlap", i, a.Pos, a.End, j, b.Pos, b.End)
		}
	}
}
