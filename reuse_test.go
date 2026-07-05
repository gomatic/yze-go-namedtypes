package namedtypes

// White-box test for the visibility contract on a shape the analysistest
// corpus cannot express: every position the corpus produces lies inside one of
// the pass's file scopes, so the no-enclosing-scope guard is reachable only
// through the API. The contract it pins: a position outside every file scope
// resolves nothing, so the reuse fix is skipped rather than emitted with a
// reference that cannot be checked.

import (
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVisibleAtOutsideAnyFileScope(t *testing.T) {
	pkg := types.NewPackage("example.test/p", "p")
	named := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "N", nil), types.Typ[types.String], nil)
	assert.False(t, visibleAt(pkg, named.Obj(), token.Pos(42)))
}
