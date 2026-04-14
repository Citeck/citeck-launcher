package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHandleSetAdminPassword_RotatesAllFourAdminUIs is a source-level guard
// test that asserts the admin-password handler rotates the human
// administrator password across all four admin UIs (ecos-app realm admin,
// master realm admin, RabbitMQ admin, PgAdmin admin). It does NOT exercise
// the handler end-to-end (that would require a live docker + keycloak +
// rabbitmq + pgadmin stack) — instead it parses routes_admin_password.go
// and checks that each expected target appears in the rotation flow.
//
// The test exists because the launcher-Keycloak SA split (2.1.0) means
// rotating the master realm admin is now safe — the launcher uses the
// stable `citeck` SA, not the human admin. Previously master realm was
// deliberately skipped; now it is included. A future refactor that
// silently drops any of the four targets will be caught here.
func TestHandleSetAdminPassword_RotatesAllFourAdminUIs(t *testing.T) {
	src, err := os.ReadFile("routes_admin_password.go")
	require.NoError(t, err)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "routes_admin_password.go", src, parser.ParseComments)
	require.NoError(t, err)

	var handler *ast.FuncDecl
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name.Name == "handleSetAdminPassword" {
			handler = fn
			break
		}
	}
	require.NotNil(t, handler, "handleSetAdminPassword not found")

	// Collect literals and identifier usages in the handler body.
	var body strings.Builder
	ast.Inspect(handler, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BasicLit:
			body.WriteString(v.Value)
			body.WriteByte('\n')
		case *ast.SelectorExpr:
			body.WriteString(v.Sel.Name)
			body.WriteByte('\n')
		}
		return true
	})
	haystack := body.String()

	// Target 1: ecos-app realm admin — driven through resetKeycloakAdminPassword.
	require.Contains(t, haystack, "resetKeycloakAdminPassword",
		"handler must rotate ecos-app realm admin via resetKeycloakAdminPassword")

	// Target 2: master realm admin — driven directly via kcadmSetPassword
	// with realm "master". Walk the AST and find a kcadmSetPassword call
	// whose 3rd positional argument is the string literal "master".
	var masterRealmTargeted bool
	ast.Inspect(handler, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "kcadmSetPassword" || len(call.Args) < 3 {
			return true
		}
		if lit, ok := call.Args[2].(*ast.BasicLit); ok &&
			lit.Kind == token.STRING && strings.Trim(lit.Value, `"`) == "master" {
			masterRealmTargeted = true
			return false
		}
		return true
	})
	require.True(t, masterRealmTargeted,
		"handler must call kcadmSetPassword(ctx, containerID, \"master\", pwd) to rotate the Keycloak master realm admin (new policy in 2.1.0)")

	// Target 3: RabbitMQ admin — via rabbitmqctl change_password.
	require.Contains(t, haystack, `"rabbitmqctl"`,
		"handler must rotate RabbitMQ admin via rabbitmqctl")
	require.Contains(t, haystack, `"change_password"`,
		"handler must use rabbitmqctl change_password")

	// Target 4: PgAdmin admin — via setup.py update-user.
	require.Contains(t, haystack, `"update-user"`,
		"handler must rotate PgAdmin admin via setup.py update-user")

	// Target 5: the new password must be persisted to the `_admin_password`
	// system secret so daemon restarts see the rotated value.
	require.Contains(t, haystack, `"_admin_password"`,
		"handler must persist the new password to the _admin_password secret")
}

// TestResetKeycloakAdminPassword_TargetsEcosApp verifies that the helper
// the handler delegates to still calls set-password for the ecos-app
// realm. The master realm set-password is driven by the caller in a
// separate phase (see handleSetAdminPassword) — both phases are fatal on
// failure, but keeping them separate lets the caller emit a master-
// specific error message that tells the user to retry via
// `citeck setup admin-password` since ecos-app is already rotated.
// This test ensures the split stays — if master realm rotation ever
// migrates into this helper, the error-message split must be revisited.
func TestResetKeycloakAdminPassword_TargetsEcosApp(t *testing.T) {
	src, err := os.ReadFile("routes_admin_password.go")
	require.NoError(t, err)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "routes_admin_password.go", src, parser.ParseComments)
	require.NoError(t, err)

	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if d.Name.Name == "resetKeycloakAdminPassword" {
			fn = d
			break
		}
	}
	require.NotNil(t, fn, "resetKeycloakAdminPassword not found")

	// Walk the AST looking for a call to d.kcadmSetPassword(...) and check
	// its realm argument (3rd positional: ctx, containerID, realm, pwd).
	var setPasswordRealms []string
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "kcadmSetPassword" {
			return true
		}
		if len(call.Args) < 3 {
			return true
		}
		if lit, ok := call.Args[2].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			setPasswordRealms = append(setPasswordRealms, strings.Trim(lit.Value, `"`))
		}
		return true
	})

	require.Contains(t, setPasswordRealms, "ecos-app",
		"resetKeycloakAdminPassword must still call kcadmSetPassword for the ecos-app realm")
	require.NotContains(t, setPasswordRealms, "master",
		"resetKeycloakAdminPassword must NOT call kcadmSetPassword for master — the handler drives that in a separate (also-fatal) phase so it can emit a master-specific retry message")
}
