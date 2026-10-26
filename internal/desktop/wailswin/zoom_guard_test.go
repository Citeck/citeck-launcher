package wailswin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoHookOnWindowRuntimeReady: the page never loads Wails' JS runtime, so
// WindowRuntimeReady never fires and anything hooked on it never runs — that
// is how the main window went without its UI zoom on macOS for every release
// up to 2.17.3. No build tag on purpose: this runs in CI on Linux without
// gtk3, where the rest of the package is excluded.
func TestNoHookOnWindowRuntimeReady(t *testing.T) {
	dirs := []string{".", filepath.Join("..", "..", "..", "cmd", "citeck-desktop")}
	scanned := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			scanned++
			ast.Inspect(file, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "WindowRuntimeReady" {
					t.Errorf("%s uses WindowRuntimeReady, which never fires: the page does not load Wails' JS runtime",
						fset.Position(sel.Pos()))
				}
				return true
			})
		}
	}
	if scanned < 3 {
		t.Fatalf("scanned only %d files; the guard is not looking where the windows are created", scanned)
	}
}
