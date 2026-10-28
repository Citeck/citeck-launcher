package desktop_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Wails queues ExecJS until runtime readiness is marked. Every wrapper-created
// window must pass through MarkRuntimeReadyOnFirstLoad so older daemon pages
// without a handshake work too. The v2.17.3 frontend already sends the message;
// these guards check wrapper independence, not the absence of that frontend
// path. See docs/desktop-webview.md.
//
// These checks read source rather than run Wails: cmd/citeck-desktop and
// wailswin only build with the desktop / gtk3 tags, which the Linux CI test job
// does not set, and a window cannot be created without a running application.

// TestEveryWebviewWindowIsMarkedRuntimeReady finds every `<x>.Window.New…(…)`
// call in the wrapper and requires the window it returns to be passed to
// MarkRuntimeReadyOnFirstLoad in the same function.
func TestEveryWebviewWindowIsMarkedRuntimeReady(t *testing.T) {
	created := 0
	for _, dir := range []string{
		filepath.Join("..", "..", "cmd", "citeck-desktop"),
		"wailswin",
	} {
		for _, file := range goSources(t, dir) {
			for _, fn := range file.Decls {
				decl, ok := fn.(*ast.FuncDecl)
				if !ok || decl.Body == nil {
					continue
				}
				for _, win := range createdWindows(decl.Body) {
					created++
					require.Truef(t, marksRuntimeReady(decl.Body, win),
						"%s: %s creates webview window %q without passing it to MarkRuntimeReadyOnFirstLoad — "+
							"nothing sent to it through ExecJS (reloads, bridged daemon events) would ever run",
						dir, decl.Name.Name, win)
				}
			}
		}
	}
	// The main window and the secondary windows: a matcher that finds nothing
	// would pass vacuously.
	require.GreaterOrEqual(t, created, 2, "expected to find the main window and the secondary-window manager")
}

// TestRuntimeReadyMessageIsTheOneWailsHandles pins the message the wrapper
// posts on a window's behalf to the case in the pinned Wails' HandleMessage
// that releases the ExecJS queue. If an upgrade renamed it, the wrapper would
// be back to queuing forever with nothing failing anywhere.
func TestRuntimeReadyMessageIsTheOneWailsHandles(t *testing.T) {
	message := stringConst(t, filepath.Join("wailswin", "runtime_ready.go"), "runtimeReadyMessage")

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/wailsapp/wails/v3").Output()
	wailsDir := strings.TrimSpace(string(out))
	if err != nil || wailsDir == "" {
		t.Skipf("Wails module source not in the module cache (err=%v)", err)
	}
	raw, err := os.ReadFile(filepath.Join(wailsDir, "pkg", "application", "webview_window.go")) //nolint:gosec // path from go list
	require.NoError(t, err)
	src := string(raw)

	caseLabel := "case message == " + strconv.Quote(message) + ":"
	start := strings.Index(src, caseLabel)
	require.NotEqualf(t, -1, start, "Wails' HandleMessage no longer has %q", caseLabel)
	body := src[start+len(caseLabel):]
	if next := strings.Index(body, "\n\tcase "); next >= 0 {
		body = body[:next]
	}
	require.Containsf(t, body, "runtimeLoaded = true",
		"Wails' %q case no longer releases the ExecJS queue", message)
}

func goSources(t *testing.T, dir string) []*ast.File {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	var files []*ast.File
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		files = append(files, f)
	}
	require.NotEmpty(t, files, "no Go sources in %s", dir)
	return files
}

// createdWindows returns the names assigned the result of `<x>.Window.New…()`.
func createdWindows(body *ast.BlockStmt) []string {
	var names []string
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(method.Sel.Name, "New") {
			return true
		}
		if mgr, ok := method.X.(*ast.SelectorExpr); !ok || mgr.Sel.Name != "Window" {
			return true
		}
		if id, ok := assign.Lhs[0].(*ast.Ident); ok {
			names = append(names, id.Name)
		}
		return true
	})
	return names
}

func marksRuntimeReady(body *ast.BlockStmt, win string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		var name string
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		if arg, ok := call.Args[0].(*ast.Ident); ok && name == "MarkRuntimeReadyOnFirstLoad" && arg.Name == win {
			found = true
		}
		return !found
	})
	return found
}

func stringConst(t *testing.T, path, name string) string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if id.Name != name || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				require.Truef(t, ok && lit.Kind == token.STRING, "%s must be a string literal", name)
				v, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				return v
			}
		}
	}
	t.Fatalf("const %s not found in %s", name, path)
	return ""
}
