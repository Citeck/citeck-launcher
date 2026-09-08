package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An error CODE is what a client branches on, so two constants sharing one
// wire value make those branches undecidable — and the mistake is invisible at
// a glance in a 40-entry const block where the names differ and the strings do
// not. This walks the real declarations rather than a hand-kept list, so a
// copy-pasted value fails the build the moment it is added.
func TestErrorCodeValuesAreUnique(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "dto.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	seen := map[string]string{} // wire value → constant name
	count := 0
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "ErrCode") || i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, uerr := strconv.Unquote(lit.Value)
			require.NoError(t, uerr)
			require.NotEmpty(t, value, "%s has an empty error code", name.Name)
			if prev, dup := seen[value]; dup {
				t.Errorf("error code %q is declared twice: %s and %s", value, prev, name.Name)
			}
			seen[value] = name.Name
			count++
		}
		return true
	})
	require.NotZero(t, count, "no ErrCode constants found — the walk is broken, not the codes")
}
