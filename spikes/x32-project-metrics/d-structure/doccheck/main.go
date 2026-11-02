package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := os.Args[1]
	fset := token.NewFileSet()
	type st struct{ exp, doc int }
	by := map[string]*st{}
	var undoc []string
	filepath.Walk(root, func(p string, i os.FileInfo, err error) error {
		if err != nil || i.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, "/internal/gen/") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		dir := filepath.Dir(p)
		s := by[dir]
		if s == nil {
			s = &st{}
			by[dir] = s
		}
		rec := func(name string, doc *ast.CommentGroup) {
			if !ast.IsExported(name) {
				return
			}
			s.exp++
			if doc != nil {
				s.doc++
			} else {
				undoc = append(undoc, p+":"+name)
			}
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil {
					// methods of unexported types are not public API
					t := d.Recv.List[0].Type
					if se, ok := t.(*ast.StarExpr); ok {
						t = se.X
					}
					if id, ok := t.(*ast.Ident); ok && !ast.IsExported(id.Name) {
						continue
					}
				}
				rec(d.Name.Name, d.Doc)
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					switch sp := sp.(type) {
					case *ast.TypeSpec:
						doc := sp.Doc
						if doc == nil && len(d.Specs) == 1 {
							doc = d.Doc
						}
						rec(sp.Name.Name, doc)
					case *ast.ValueSpec:
						doc := sp.Doc
						if doc == nil {
							doc = d.Doc
						}
						if doc == nil {
							doc = sp.Comment
						}
						for _, n := range sp.Names {
							rec(n.Name, doc)
						}
					}
				}
			}
		}
		return nil
	})
	te, td := 0, 0
	fmt.Println("package,exported,documented")
	for d, s := range by {
		fmt.Printf("%s,%d,%d\n", d, s.exp, s.doc)
		te += s.exp
		td += s.doc
	}
	fmt.Fprintf(os.Stderr, "TOTAL exported %d documented %d (%.1f%%)\n", te, td, float64(td)/float64(te)*100)
	os.WriteFile(os.Args[2], []byte(strings.Join(undoc, "\n")+"\n"), 0o644)
}
