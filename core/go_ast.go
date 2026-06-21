package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

func extractGoSymbolsAST(path, content string) ([]Symbol, bool) {
	fileSet, file, ok := parseGoFile(path, content, parser.SkipObjectResolution)
	if !ok {
		return nil, false
	}

	var symbols []Symbol
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			if node.Name == nil || protectedIdentifier(node.Name.Name) {
				continue
			}
			kind := "function"
			if node.Recv != nil {
				kind = "method"
			}
			symbols = append(symbols, Symbol{
				Name:      node.Name.Name,
				Kind:      kind,
				Signature: compactSourceSpan(fileSet, content, node.Pos(), goFuncSignatureEnd(node)),
				Language:  "go",
				Path:      path,
				Line:      fileSet.Position(node.Pos()).Line,
			})
		case *ast.GenDecl:
			if node.Tok != token.TYPE {
				continue
			}
			for _, spec := range node.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || typeSpec.Name == nil || protectedIdentifier(typeSpec.Name.Name) {
					continue
				}
				symbols = append(symbols, Symbol{
					Name:      typeSpec.Name.Name,
					Kind:      "type",
					Signature: compactSourceSpan(fileSet, content, typeSpec.Pos(), typeSpec.End()),
					Language:  "go",
					Path:      path,
					Line:      fileSet.Position(typeSpec.Pos()).Line,
				})
			}
		}
	}
	return symbols, true
}

func extractGoSymbolReferencesAST(path, content string) ([]SymbolReference, bool) {
	fileSet, file, ok := parseGoFile(path, content, parser.SkipObjectResolution)
	if !ok {
		return nil, false
	}

	definedOnLine := map[int]map[string]bool{}
	if symbols, ok := extractGoSymbolsAST(path, content); ok {
		for _, symbol := range symbols {
			if definedOnLine[symbol.Line] == nil {
				definedOnLine[symbol.Line] = map[string]bool{}
			}
			definedOnLine[symbol.Line][symbol.Name] = true
		}
	}

	seen := map[string]bool{}
	var refs []SymbolReference
	appendRef := func(name string, pos token.Pos) {
		name = strings.TrimSpace(name)
		line := fileSet.Position(pos).Line
		if name == "" || definedOnLine[line][name] || protectedIdentifier(name) || commonSymbolReferenceName(name) {
			return
		}
		key := path + ":" + strconv.Itoa(line) + ":" + name
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, SymbolReference{
			Name:     name,
			Language: "go",
			Path:     path,
			Line:     line,
			Context:  sourceLine(content, line),
		})
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CallExpr:
			if name := goCallName(typed.Fun); name != "" {
				appendRef(name, typed.Fun.Pos())
			}
		case *ast.CompositeLit:
			if name := goTypeName(typed.Type); name != "" {
				appendRef(name, typed.Type.Pos())
			}
		}
		return true
	})
	return refs, true
}

func extractGoImportsAST(path, content string) ([]ImportReference, bool) {
	fileSet, file, ok := parseGoFile(path, content, parser.ImportsOnly|parser.SkipObjectResolution)
	if !ok {
		return nil, false
	}

	seen := map[string]bool{}
	var refs []ImportReference
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			importPath = strings.Trim(spec.Path.Value, `"`+"`")
		}
		alias := ""
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		line := fileSet.Position(spec.Pos()).Line
		appendImport(&refs, seen, "go", path, line, sourceLine(content, line), importPath, alias)
	}
	return refs, true
}

func extractGoTopLevelSpansAST(path, content string) ([]codeSpan, bool) {
	fileSet, file, ok := parseGoFile(path, content, parser.ParseComments|parser.SkipObjectResolution)
	if !ok {
		return nil, false
	}

	var spans []codeSpan
	for _, decl := range file.Decls {
		start := decl.Pos()
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			if typed.Doc != nil {
				start = typed.Doc.Pos()
			}
		case *ast.GenDecl:
			if typed.Doc != nil {
				start = typed.Doc.Pos()
			}
		}
		startLine := fileSet.Position(start).Line
		endLine := fileSet.Position(decl.End()).Line
		if startLine > 0 && endLine >= startLine {
			spans = append(spans, codeSpan{StartLine: startLine, EndLine: endLine})
		}
	}
	return spans, true
}

func parseGoFile(path, content string, mode parser.Mode) (*token.FileSet, *ast.File, bool) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, content, mode)
	if err != nil || file == nil {
		return fileSet, file, false
	}
	return fileSet, file, true
}

func goFuncSignatureEnd(decl *ast.FuncDecl) token.Pos {
	if decl.Body != nil {
		return decl.Body.Pos()
	}
	return decl.End()
}

func goCallName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func goTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.StarExpr:
		return goTypeName(typed.X)
	case *ast.ArrayType:
		return goTypeName(typed.Elt)
	default:
		return ""
	}
}

func compactSourceSpan(fileSet *token.FileSet, content string, start, end token.Pos) string {
	startPos := fileSet.Position(start)
	endPos := fileSet.Position(end)
	if startPos.Offset < 0 || endPos.Offset <= startPos.Offset || startPos.Offset >= len(content) {
		return strings.TrimSpace(sourceLine(content, startPos.Line))
	}
	if endPos.Offset > len(content) {
		endPos.Offset = len(content)
	}
	value := strings.TrimSpace(content[startPos.Offset:endPos.Offset])
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 240 {
		return value[:240]
	}
	return value
}

func sourceLine(content string, lineNumber int) string {
	if lineNumber <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if lineNumber > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[lineNumber-1])
}
