package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Regression guard for the systemd-restart bug where `citeck start --foreground`
// peeked at the existing daemon socket BEFORE checking the foreground flag.
// During `systemctl restart citeck`, the ExecStop'd daemon detaches but stays
// alive for a moment; the new ExecStart'd process used to find that socket,
// fall through to client mode, and exit — leaving the unit dead with orphaned
// containers.
//
// The contract: in `start` cmd's RunE, the `client.TryNew(clientOpts())` call
// MUST live inside a block guarded by `if !foreground`. We enforce this with
// an AST walk, not a string match — `strings.Contains` would happily pass if
// the guard were deleted but the comment kept.
func TestStartCmd_ForegroundSkipsClientPeek(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "start.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse start.go: %v", err)
	}

	startCmd := findFuncDecl(file, "newStartCmd")
	if startCmd == nil {
		t.Fatal("newStartCmd not found in start.go — guard is out of date")
	}
	runE := findRunEFuncLit(startCmd)
	if runE == nil {
		t.Fatal("RunE func literal not found in newStartCmd — guard is out of date")
	}

	// Only consider TryNew calls inside RunE. The waitForDaemon helper at
	// file scope also calls TryNew but for a different purpose (poll until
	// the new daemon is ready); excluding it keeps the test focused on the
	// branch that actually matters for the bug.
	tryNewCalls := findTryNewClientCalls(runE)
	if len(tryNewCalls) == 0 {
		t.Fatal("no client.TryNew(clientOpts()) call found in RunE — the start cmd has changed shape, update this guard")
	}
	for _, call := range tryNewCalls {
		if !isInsideNotForegroundBlock(runE, call) {
			t.Errorf("client.TryNew(clientOpts()) call at %s is NOT guarded by `if !foreground { ... }` — systemd restart will fail again",
				fset.Position(call.Pos()))
		}
	}
}

// findFuncDecl returns the top-level function with the given name, or nil.
func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// findRunEFuncLit locates the `RunE: func(cmd, args) error { ... }` literal
// assigned inside the cobra.Command composite literal built in newStartCmd.
// Returns nil if the structure changes — failing fast is intentional.
func findRunEFuncLit(fn *ast.FuncDecl) *ast.FuncLit {
	var out *ast.FuncLit
	ast.Inspect(fn, func(n ast.Node) bool {
		if out != nil {
			return false
		}
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "RunE" {
			return true
		}
		if lit, ok := kv.Value.(*ast.FuncLit); ok {
			out = lit
			return false
		}
		return true
	})
	return out
}

// findTryNewClientCalls returns every `client.TryNew(...)` call expression in
// the given subtree. Looking for the selector explicitly is what makes this
// robust to reordering or renaming `clientOpts()` to something else.
func findTryNewClientCalls(root ast.Node) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(root, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkg.Name == "client" && sel.Sel.Name == "TryNew" {
			out = append(out, call)
		}
		return true
	})
	return out
}

// isInsideNotForegroundBlock walks ancestors of `target` and reports whether
// any enclosing IfStmt has the condition `!foreground`. We don't accept other
// truthy conditions — the rule is narrow and the comment in start.go ties it
// specifically to the foreground flag.
func isInsideNotForegroundBlock(root ast.Node, target ast.Node) bool {
	stack := []ast.Node{}
	found := false
	ast.Inspect(root, func(n ast.Node) bool {
		if found {
			return false
		}
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		if n == target {
			for i := len(stack) - 1; i >= 0; i-- {
				ifStmt, ok := stack[i].(*ast.IfStmt)
				if !ok {
					continue
				}
				if isNotForegroundCond(ifStmt.Cond) {
					found = true
					return false
				}
			}
			return false
		}
		stack = append(stack, n)
		return true
	})
	return found
}

// isNotForegroundCond returns true for the bare `!foreground` expression. We
// keep this strict — `!foreground && something` would technically still skip
// the peek, but accepting it invites someone to add a second clause that
// silently re-enables the bad branch.
func isNotForegroundCond(e ast.Expr) bool {
	unary, ok := e.(*ast.UnaryExpr)
	if !ok || unary.Op != token.NOT {
		return false
	}
	id, ok := unary.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == "foreground"
}
