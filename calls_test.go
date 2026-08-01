package namedtypes

import (
	"go/ast"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCallArgumentsRefusesAMismatchedArity names callArguments' claim. A call
// whose argument count differs from the signature's flattened count is either a
// spread `f(g())`, where the argument to wrap is not a syntactic argument at
// all, or a variadic call, where the flagged position may hold several.
// Returning a partial list rather than refusing would wrap the wrong expression.
func TestCallArgumentsRefusesAMismatchedArity(t *testing.T) {
	t.Parallel()
	calls := []*ast.CallExpr{
		{Args: []ast.Expr{ast.NewIdent("a"), ast.NewIdent("b")}},
		{Args: []ast.Expr{ast.NewIdent("c"), ast.NewIdent("d")}},
	}

	args, ok := callArguments(calls, 1, 2)
	require.True(t, ok)
	require.Len(t, args, 2)
	assert.Equal(t, "b", args[0].(*ast.Ident).Name)
	assert.Equal(t, "d", args[1].(*ast.Ident).Name)

	_, ok = callArguments(append(calls, &ast.CallExpr{Args: []ast.Expr{ast.NewIdent("pair")}}), 1, 2)
	assert.False(t, ok, "one call of the wrong arity disqualifies the whole rewrite")

	_, ok = callArguments(nil, 0, 1)
	assert.True(t, ok, "no calls is not a mismatch")
}
