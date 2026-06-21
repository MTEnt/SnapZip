package core

import (
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func extractTopLevelSpansTreeSitter(language, content string) ([]codeSpan, bool) {
	// 1. Detect language grammar using name or fallback suffix
	entry := grammars.DetectLanguageByName(NormalizeLanguage(language))
	if entry == nil {
		entry = grammars.DetectLanguage("file." + language)
		if entry == nil {
			return nil, false
		}
	}
	lang := entry.Language()
	if lang == nil {
		return nil, false
	}

	// 2. Parse the content
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse([]byte(content))
	if err != nil {
		return nil, false
	}

	root := tree.RootNode()
	if root.ChildCount() == 0 {
		return nil, false
	}

	// 3. Collect top-level declaration code spans
	var spans []codeSpan
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		childType := child.Type(lang)

		// Exclude standalone comments from splitting blocks
		if childType == "comment" {
			continue
		}

		startLine := int(child.StartPoint().Row) + 1
		endLine := int(child.EndPoint().Row) + 1
		if startLine > 0 && endLine >= startLine {
			spans = append(spans, codeSpan{StartLine: startLine, EndLine: endLine})
		}
	}
	return spans, true
}
