package daemon

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Force Update has exactly one way to bypass the git pull throttle:
// Resolver.WithForcePull. The obvious-looking alternative — handing the
// resolver a WorkspaceRepoOpts with PullPeriod: 0 — reads like a force and is
// not one: workspaceRepoSettings only adopts the option when it is > 0, so
// that a workspace which configures no period still gets the default. A zero
// there is therefore "not configured", and the default throttle stays.
//
// That is how the button came to answer 200 without fetching anything: for up
// to defaultPullPeriod after the last sync, pressing Force Update produced no
// git traffic at all, and a bundle version pushed a minute earlier stayed
// invisible in the version list and to the newer-bundle indicator.
//
// resolveActiveWorkspaceConfig performs a real clone/pull, so a unit test
// cannot drive it (the same reason TestNewerBundleFillNeverReacquiresConfigMu
// asserts on source). This asserts on the production source instead: the force
// path must call WithForcePull, and must not reintroduce the zero-period
// spelling that does nothing.
func TestForceUpdateActuallyForcesTheWorkspacePull(t *testing.T) {
	fn := parseFuncDecl(t, "routes_workspace.go", "resolveActiveWorkspaceConfig")

	require.NotEqual(t, token.NoPos, callPos(fn, "WithForcePull"),
		"resolveActiveWorkspaceConfig no longer calls WithForcePull — then Force Update and the "+
			"workspace self-heal are throttled by the default pull period, and a freshly pushed "+
			"bundle version stays invisible for up to that long. A zero PullPeriod on the options "+
			"is NOT a substitute: workspaceRepoSettings ignores it.")

	assert.Empty(t, zeroPullPeriodAssignments(fn),
		"resolveActiveWorkspaceConfig sets PullPeriod = 0 again. That spelling looks like a force "+
			"and is a no-op (workspaceRepoSettings takes the option only when > 0); WithForcePull "+
			"is the one that works.")
}

// zeroPullPeriodAssignments returns the positions of every `<x>.PullPeriod = 0`
// assignment in fn.
func zeroPullPeriodAssignments(fn *ast.FuncDecl) []token.Pos {
	var out []token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "PullPeriod" || i >= len(assign.Rhs) {
				continue
			}
			if lit, ok := assign.Rhs[i].(*ast.BasicLit); ok && lit.Kind == token.INT && lit.Value == "0" {
				out = append(out, assign.Pos())
			}
		}
		return true
	})
	return out
}
