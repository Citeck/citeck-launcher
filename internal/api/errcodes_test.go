package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
	// Every non-test file of the package, not dto.go alone: today all 32 codes
	// are declared there, but a duplicate introduced in a NEW file is exactly
	// the case a walk pinned to one filename cannot see, and the whole point of
	// walking declarations instead of keeping a list is that nobody has to
	// remember to update it.
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		require.NoError(t, perr, name)
		files = append(files, parsed)
	}
	require.NotEmpty(t, files)

	seen := map[string]string{} // wire value → constant name
	count := 0
	inspect := func(n ast.Node) bool {
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
	}
	for _, f := range files {
		ast.Inspect(f, inspect)
	}
	require.NotZero(t, count, "no ErrCode constants found — the walk is broken, not the codes")
}
