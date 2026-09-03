package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestEmbeddedToolCatalogOwnsExactPublicSurface(t *testing.T) {
	catalog, err := loadToolCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(catalog.Tools), 83; got != want {
		t.Fatalf("tool count=%d want=%d", got, want)
	}
	required := []string{
		"gpt_agent_status", "gpt_agent_self_check", "gpt_agent_search_text",
		"gpt_agent_lsp_definition", "gpt_agent_debug_python_start", "gpt_agent_run_command",
		"gpt_agent_learning_stats", "gpt_agent_project_inspect", "gpt_agent_job_start",
	}
	for _, name := range required {
		if !catalog.has(name) {
			t.Fatalf("embedded catalog missing %s", name)
		}
	}
	for _, name := range nativeReadToolNames {
		if !catalog.has(name) {
			t.Fatalf("native tool missing from catalog: %s", name)
		}
	}
}

func TestEveryNonJobCatalogToolHasGoNativeHandler(t *testing.T) {
	catalog, err := loadToolCatalog()
	if err != nil {
		t.Fatal(err)
	}
	native := &nativeTools{}
	count := 0
	for _, name := range catalog.names() {
		if strings.HasPrefix(name, "gpt_agent_job_") {
			continue
		}
		count++
		if !native.supports(name) {
			t.Fatalf("catalog tool lacks Go-native handler: %s", name)
		}
	}
	if count != 79 {
		t.Fatalf("non-job catalog count=%d want=79", count)
	}
}

func dispatcherCases(t *testing.T, filename, functionName string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	found := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != functionName || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err == nil && strings.HasPrefix(value, "gpt_agent_") {
					found[value] = true
				}
			}
			return true
		})
		return found
	}
	t.Fatalf("dispatcher function %s not found in %s", functionName, filename)
	return nil
}

func TestEveryDeclaredNativeToolHasDispatcherCase(t *testing.T) {
	cases := dispatcherCases(t, "native_tools.go", "call")
	for name := range dispatcherCases(t, "native_extra.go", "callNativeExtra") {
		cases[name] = true
	}
	native := &nativeTools{}
	for _, name := range native.names() {
		if !cases[name] {
			t.Fatalf("declared Go-native tool has no dispatcher case: %s", name)
		}
	}
	if got, want := len(cases), len(native.names()); got != want {
		t.Fatalf("dispatcher case count=%d native names=%d", got, want)
	}
}
