package storageboundary

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDomainPackagesDoNotExecuteGenericSQLOrUseRawNotFound(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test location")
	}
	internalRoot := filepath.Dir(filepath.Dir(current))
	packages := []string{"auth", "models", "instances", "settings", "downloads", "modelimports", "observability", "litellm"}
	for _, pkg := range packages {
		dir := filepath.Join(internalRoot, pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.Contains(name, "store.go") || strings.Contains(name, "store_sql.go") || name == "sql_factory.go" {
				continue
			}
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imp := range file.Imports {
				value, _ := strconv.Unquote(imp.Path.Value)
				if value == "database/sql" {
					t.Errorf("%s imports database/sql outside an SQL adapter; keep SQL persistence details behind domain stores", filepath.Join(pkg, name))
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Store" {
					return true
				}
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "database" {
					t.Errorf("%s depends on generic database.Store outside an SQL adapter/factory", filepath.Join(pkg, name))
				}
				return true
			})
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "ExecContext", "QueryContext", "QueryRowContext":
					t.Errorf("%s directly executes generic SQL outside a store adapter", filepath.Join(pkg, name))
				}
				return true
			})
		}
	}
}
