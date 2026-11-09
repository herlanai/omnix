package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestGenerateSkeletonsPreservesMetadataOutsideComments(t *testing.T) {
	row := tcRow{
		ID:   "TC-M07-04",
		Kind: "集成",
		Name: "客户端 \"quoted\"\nnext line \\ path",
		File: "测试文件.go",
	}
	data, err := generateSkeletons([]tcRow{row})
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "generated_test.go", data, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range file.Comments {
		if strings.ContainsFunc(group.Text(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Fatalf("generated comment is not English: %s", group.Text())
		}
	}
	var skipMessage string
	var testName string
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			testName = n.Name.Name
		case *ast.CallExpr:
			selector, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Skip" || len(n.Args) != 1 {
				return true
			}
			literal, ok := n.Args[0].(*ast.BasicLit)
			if !ok {
				t.Fatal("skip metadata must be a quoted string")
			}
			skipMessage, err = strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
		return true
	})
	if testName != "TestTCM07_04" {
		t.Fatalf("test identifier changed: %s", testName)
	}
	for _, metadata := range []string{row.ID, row.Kind, row.Name, row.File} {
		if !strings.Contains(skipMessage, metadata) {
			t.Fatalf("skip message lost metadata %q: %q", metadata, skipMessage)
		}
	}
}

func TestGenerateSkeletonsEmpty(t *testing.T) {
	data, err := generateSkeletons(nil)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "generated_test.go", data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Decls) != 0 {
		t.Fatal("empty generation must not introduce unused imports or tests")
	}
}
