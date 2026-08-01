package namedtypes

// White-box tests for the fix-eligibility contract on shapes the analysistest
// corpus cannot express: a bodyless function declaration (an assembly stub
// would not type-check in testdata) and a _test.go source path (the production
// driver loads packages without test files, so testdata cannot carry one
// without duplicating diagnostics across package variants).

import (
	"go/ast"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eligibleFixture yields a minimal unexported single-string-parameter function
// declaration that satisfies every fixEligible condition.
func eligibleFixture() (*ast.FuncDecl, *ast.Field) {
	field := &ast.Field{
		Names: []*ast.Ident{ast.NewIdent("s")},
		Type:  ast.NewIdent("string"),
	}
	fn := &ast.FuncDecl{
		Name: ast.NewIdent("lower"),
		Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{field}}},
		Body: &ast.BlockStmt{},
	}
	return fn, field
}

func TestFixEligible(t *testing.T) {
	tests := []struct {
		mutate   func(fn *ast.FuncDecl, field *ast.Field)
		name     string
		path     sourcePath
		eligible bool
	}{
		{
			name:     "eligible unexported function in a non-test file",
			path:     "a.go",
			mutate:   func(*ast.FuncDecl, *ast.Field) {},
			eligible: true,
		},
		{
			name:     "exported function",
			path:     "a.go",
			mutate:   func(fn *ast.FuncDecl, _ *ast.Field) { fn.Name = ast.NewIdent("Upper") },
			eligible: false,
		},
		{
			name:     "test file",
			path:     "a_test.go",
			mutate:   func(*ast.FuncDecl, *ast.Field) {},
			eligible: false,
		},
		{
			name:     "bodyless declaration",
			path:     "a.go",
			mutate:   func(fn *ast.FuncDecl, _ *ast.Field) { fn.Body = nil },
			eligible: false,
		},
		{
			name: "shared multi-name field",
			path: "a.go",
			mutate: func(_ *ast.FuncDecl, field *ast.Field) {
				field.Names = append(field.Names, ast.NewIdent("t"))
			},
			eligible: false,
		},
		{
			name:     "unnamed parameter",
			path:     "a.go",
			mutate:   func(_ *ast.FuncDecl, field *ast.Field) { field.Names = nil },
			eligible: false,
		},
		{
			name: "variadic parameter",
			path: "a.go",
			mutate: func(_ *ast.FuncDecl, field *ast.Field) {
				field.Type = &ast.Ellipsis{Elt: field.Type}
			},
			eligible: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, field := eligibleFixture()
			tt.mutate(fn, field)
			assert.Equal(t, tt.eligible, fixEligible(tt.path, fn, field))
		})
	}
}

// TestMintedTypeRequiresAnExactlyMatchingUnderlying names mintedType's claim:
// the package-level type called name qualifies only when its underlying is
// EXACTLY the primitive. Each rejected shape is a distinct way to emit a wrong
// fix — reusing an alias retypes the parameter to the primitive it already had,
// producing a no-op fix the user then commits as if it meant something, and
// reusing a type over a different underlying writes source that does not build.
func TestMintedTypeRequiresAnExactlyMatchingUnderlying(t *testing.T) {
	t.Parallel()
	pkg, _, _ := checked(t, `package p
type Skeleton string
type AliasOfPrimitive = string
type AliasOfNamed = Skeleton
type Different int
type Generic[T any] string
type Instantiated = Generic[int]
const NotAType = 1
var _ Instantiated
`)
	str := types.Typ[types.String]

	named, ok := mintedType(pkg, "Skeleton", str)
	require.True(t, ok, "a defined type over exactly the primitive qualifies")
	assert.Equal(t, "Skeleton", named.Obj().Name())

	for _, tc := range []struct {
		name identName
		why  string
	}{
		{name: "AliasOfPrimitive", why: "an alias to the primitive would retype the parameter to what it already was"},
		{name: "AliasOfNamed", why: "an alias is not the type it names; reusing it writes a name the fix never checked"},
		{name: "Different", why: "a different underlying type does not compile"},
		{name: "Generic", why: "a generic ORIGIN takes a type argument, so naming it bare does not compile"},
		{name: "Instantiated", why: "an instantiation cannot be named as a plain parameter type either"},
		{name: "NotAType", why: "a constant is not a type"},
		{name: "Absent", why: "an undeclared name resolves to nothing"},
	} {
		name, why := tc.name, tc.why
		_, ok := mintedType(pkg, name, str)
		assert.False(t, ok, "%s must not qualify as a minted type for string: %s", name, why)
	}
}

// TestUnsafeUseDetectsEveryRetypeHazard names unsafeUse's claim. Each shape it
// must catch breaks a DIFFERENT way once the parameter is retyped: an
// assignment stores a primitive into a named-typed variable, an inc/dec is an
// assignment in disguise, a `=`-form range clause assigns on every iteration,
// and taking the address yields a *N where the code expects a *string. Missing
// any one of them means --fix writes source that does not compile.
func TestUnsafeUseDetectsEveryRetypeHazard(t *testing.T) {
	t.Parallel()
	_, info, file := checked(t, `package p
func assigned(s string) string { s = "x"; return s }
func incremented(n int) int { n++; return n }
func rangeAssigned(s string) string { for _, s = range []string{"a"} { continue }; return s }
func addressed(s string) *string { return &s }
func readOnly(s string) bool { return s == "" }
func copiedThenWritten(s string) string { t := s; t = "x"; return t }
`)
	for _, tc := range []struct {
		fn     string
		unsafe bool
	}{
		{fn: "assigned", unsafe: true},
		{fn: "incremented", unsafe: true},
		{fn: "rangeAssigned", unsafe: true},
		{fn: "addressed", unsafe: true},
		{fn: "readOnly", unsafe: false},
		{fn: "copiedThenWritten", unsafe: false},
	} {
		fn, field := funcNamed(t, file, tc.fn)
		assert.Equal(t, tc.unsafe, unsafeUse(info, fn.Body, info.Defs[field.Names[0]]), "unsafeUse(%s)", tc.fn)
	}
}

// TestParamUsesExcludesTheDeclaringIdentifier names paramUses' claim. The fix
// wraps every identifier it returns in a conversion, so the declaring
// identifier appearing here would produce `func f(string(s) N)` — not Go.
func TestParamUsesExcludesTheDeclaringIdentifier(t *testing.T) {
	t.Parallel()
	_, info, file := checked(t, `package p
func twice(s string) string { return s + s }
func unused(s string) bool { return true }
`)
	fn, field := funcNamed(t, file, "twice")
	uses := paramUses(info, fn.Body, info.Defs[field.Names[0]])
	assert.Len(t, uses, 2, "both body uses, and not the parameter's declaration")
	for _, use := range uses {
		assert.NotEqual(t, field.Names[0].Pos(), use.Pos(), "the declaring ident is a definition, not a use")
	}

	fn, field = funcNamed(t, file, "unused")
	assert.Empty(t, paramUses(info, fn.Body, info.Defs[field.Names[0]]))
}

// TestMintedTypeRejectsALegacyAliasObject reaches mintedType's IsAlias guard.
// Source cannot reach it on the current toolchain: with the default alias
// representation an alias declaration yields a *types.Alias, so the assertion
// to *types.Named fails first. Under GODEBUG=gotypesalias=0 — and in any
// go/types object graph built by hand, as tools that synthesise packages do —
// an alias TypeName carries the aliased *types.Named directly, and only this
// guard stands between the fix and retyping a parameter to a name that is not
// the type it appears to be.
func TestMintedTypeRejectsALegacyAliasObject(t *testing.T) {
	t.Parallel()
	pkg := types.NewPackage("example.test/p", "p")
	skeleton := types.NewNamed(
		types.NewTypeName(token.NoPos, pkg, "Skeleton", nil),
		types.Typ[types.String], nil,
	)
	pkg.Scope().Insert(skeleton.Obj())
	pkg.Scope().Insert(types.NewTypeName(token.NoPos, pkg, "LegacyAlias", skeleton))

	_, ok := mintedType(pkg, "Skeleton", types.Typ[types.String])
	require.True(t, ok, "the aliased type itself still qualifies")

	_, ok = mintedType(pkg, "LegacyAlias", types.Typ[types.String])
	assert.False(t, ok, "an alias object must be refused however its type is represented")
}

// TestIsGenericRejectsAParameterisedOrigin names isGeneric's claim that a
// parameterised type cannot be named bare in a parameter's type. The generic
// ORIGIN is the reachable case and the one that occurs: a package-scope lookup
// returns it carrying type parameters and no type arguments, so a guard written
// against TypeArgs lets it through and --fix emits `func f(p Dir)` for a
// `type Dir[T any] string` — source that does not compile.
func TestIsGenericRejectsAParameterisedOrigin(t *testing.T) {
	t.Parallel()
	pkg, _, _ := checked(t, `package p
type Plain string
type Origin[T any] string
type Derived Origin[int]
`)
	for _, tc := range []struct {
		name    string
		generic bool
	}{
		{name: "Plain", generic: false},
		{name: "Origin", generic: true},
		{name: "Derived", generic: false},
	} {
		named, ok := pkg.Scope().Lookup(tc.name).(*types.TypeName).Type().(*types.Named)
		require.True(t, ok, "%s must resolve to a defined type", tc.name)
		assert.Equal(t, tc.generic, isGeneric(named),
			"%s: a fresh defined type over a generic instantiation is reusable; the origin is not", tc.name)
	}
}
