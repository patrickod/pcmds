package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s <directory>", os.Args[0])
	}

	dir := os.Args[1]
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".go") {
			analyzeFile(path)
		}
		return nil
	})

	if err != nil {
		log.Fatalf("Error walking the path %q: %v\n", dir, err)
	}
}

func analyzeFile(filename string) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.AllErrors)
	if err != nil {
		log.Printf("Failed to parse file %s: %v\n", filename, err)
		return
	}

	ast.Inspect(node, func(n ast.Node) bool {
		var fn *ast.FuncType
		var body *ast.BlockStmt

		switch x := n.(type) {
		case *ast.FuncDecl:
			fn = x.Type
			body = x.Body
		case *ast.FuncLit:
			fn = x.Type
			body = x.Body
		default:
			return true
		}

		if fn.Results != nil || len(fn.Params.List) != 2 {
			return true
		}

		isHandler, requestVarName := isHTTPHandlerFunc(fn.Params.List)
		if !isHandler {
			return true
		}

		ast.Inspect(body, func(n ast.Node) bool {
			binExpr, ok := n.(*ast.BinaryExpr)
			if !ok {
				return true
			}

			// Check if the binary expression is an equality check
			if binExpr.Op != token.EQL {
				return true
			}

			// Check if the left side of the equality is r.URL.Scheme
			selector, ok := binExpr.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			if sel, ok := selector.X.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "r" && sel.Sel.Name == "URL" && selector.Sel.Name == "Scheme" {
					// Check if the right side of the equality is the string literal "https"
					lit, ok := binExpr.Y.(*ast.BasicLit)
					if !ok {
						return true
					}
					if lit.Kind != token.STRING {
						return true
					}
					if lit.Value == `"https"` {
						log.Printf("Found r.URL.Scheme == \"https\"  comparison in HTTP handler at %s\n", fset.Position(selector.Pos()))
					}
				}
			}

			return true
		})

		return true
	})
}

// isHTTPHandlerFunc checks if the function is an HTTP handler function.
// It returns true if the function has the following signature:
// func(http.ResponseWriter, *http.Request)
// The second argument is the name of the *http.Request parameter.
func isHTTPHandlerFunc(params []*ast.Field) (bool, string) {
	if len(params) != 2 {
		return false, ""
	}

	if len(params[0].Names) != 1 || len(params[1].Names) != 1 {
		return false, ""
	}

	if params[0].Names[0].Name != "w" || params[1].Names[0].Name != "r" {
		return false, ""
	}

	if !isType(params[0].Type, "http.ResponseWriter") {
		return false, ""
	}

	if !isType(params[1].Type, "*http.Request") {
		return false, ""
	}
	return true, params[1].Names[0].Name
}

func isType(expr ast.Expr, typeName string) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == typeName
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			n := x.Name + "." + t.Sel.Name
			return n == typeName
		}
	case *ast.StarExpr:
		if sel, ok := t.X.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				n := "*" + x.Name + "." + sel.Sel.Name
				return n == typeName
			}
		}
	}
	return false
}
