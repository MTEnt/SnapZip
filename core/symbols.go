package core

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Symbol struct {
	ID        int    `json:"id,omitempty"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
	Language  string `json:"language"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
}

type SymbolReference struct {
	ID       int    `json:"id,omitempty"`
	Name     string `json:"name"`
	Language string `json:"language"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Context  string `json:"context,omitempty"`
}

type RepoMapFile struct {
	Path     string   `json:"path"`
	Language string   `json:"language"`
	Symbols  []Symbol `json:"symbols,omitempty"`
}

type RepoMap struct {
	Files []RepoMapFile `json:"files"`
}

type SymbolContext struct {
	Query       string            `json:"query"`
	Definitions []Symbol          `json:"definitions"`
	References  []SymbolReference `json:"references"`
}

var symbolPatterns = map[string][]symbolPattern{
	"go": {
		{kind: "function", re: regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\([^)]*\)`)},
		{kind: "type", re: regexp.MustCompile(`^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(?:struct|interface|func|\w+)`)},
	},
	"py": {
		{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)\s*[:(]`)},
		{kind: "function", re: regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
	},
	"rb": {
		{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_:]*)`)},
		{kind: "module", re: regexp.MustCompile(`^\s*module\s+([A-Za-z_][A-Za-z0-9_:]*)`)},
		{kind: "method", re: regexp.MustCompile(`^\s*def\s+([A-Za-z_][A-Za-z0-9_!?=]*)`)},
	},
	"js":  jsLikeSymbolPatterns(),
	"jsx": jsLikeSymbolPatterns(),
	"ts":  jsLikeSymbolPatterns(),
	"tsx": jsLikeSymbolPatterns(),
	"java": {
		{kind: "class", re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|abstract\s+|final\s+)*class\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{kind: "interface", re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)*interface\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{kind: "method", re: regexp.MustCompile(`^\s*(?:public|private|protected|static|final|async|\s)+[A-Za-z0-9_<>\[\], ?]+\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
	},
	"rs": {
		{kind: "function", re: regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
		{kind: "type", re: regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|enum|trait)\s+([A-Za-z_][A-Za-z0-9_]*)`)},
	},
	"php": {
		{kind: "class", re: regexp.MustCompile(`^\s*(?:abstract\s+|final\s+)?class\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{kind: "function", re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|static\s+)*function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
	},
	"swift": {
		{kind: "type", re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?(?:class|struct|enum|protocol)\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{kind: "function", re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
	},
	"kt": {
		{kind: "type", re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?(?:class|interface|object|data\s+class)\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{kind: "function", re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?fun\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
	},
}

var symbolReferenceCallPattern = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

type symbolPattern struct {
	kind string
	re   *regexp.Regexp
}

func jsLikeSymbolPatterns() []symbolPattern {
	return []symbolPattern{
		{kind: "class", re: regexp.MustCompile(`^\s*(?:export\s+default\s+|export\s+)?class\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{kind: "function", re: regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
		{kind: "function", re: regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:async\s*)?\(`)},
		{kind: "function", re: regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:async\s+)?[A-Za-z_][A-Za-z0-9_]*\s*=>`)},
		{kind: "method", re: regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\([^)]*\)\s*\{`)},
	}
}

func ReplaceSymbolsForFile(db *sql.DB, language, path string, content []byte) error {
	language = NormalizeLanguage(language)
	path = strings.TrimSpace(path)
	symbols := ExtractSymbols(language, path, string(content))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM symbols WHERE path = ?", path); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM symbols_fts WHERE path = ?", path); err != nil {
		return err
	}
	for _, symbol := range symbols {
		_, err := tx.Exec(`
			INSERT INTO symbols (name, kind, signature, language, path, line)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(path, name, kind, line) DO UPDATE SET
				signature = excluded.signature,
				language = excluded.language,
				created_at = CURRENT_TIMESTAMP`,
			symbol.Name,
			symbol.Kind,
			symbol.Signature,
			symbol.Language,
			symbol.Path,
			symbol.Line,
		)
		if err != nil {
			return err
		}
		var rowID int64
		if err := tx.QueryRow(`
			SELECT id FROM symbols
			WHERE path = ? AND name = ? AND kind = ? AND line = ?`,
			symbol.Path,
			symbol.Name,
			symbol.Kind,
			symbol.Line,
		).Scan(&rowID); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO symbols_fts(rowid, name, kind, signature, language, path)
			VALUES (?, ?, ?, ?, ?, ?)`,
			rowID,
			symbol.Name,
			symbol.Kind,
			symbol.Signature,
			symbol.Language,
			symbol.Path,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ReplaceSymbolReferencesForFile(db *sql.DB, language, path string, content []byte) error {
	language = NormalizeLanguage(language)
	path = strings.TrimSpace(path)
	refs := ExtractSymbolReferences(language, path, string(content))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM symbol_refs WHERE path = ?", path); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM symbol_refs_fts WHERE path = ?", path); err != nil {
		return err
	}
	for _, ref := range refs {
		_, err := tx.Exec(`
			INSERT INTO symbol_refs (name, language, path, line, context)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(path, name, line) DO UPDATE SET
				language = excluded.language,
				context = excluded.context,
				created_at = CURRENT_TIMESTAMP`,
			ref.Name,
			ref.Language,
			ref.Path,
			ref.Line,
			ref.Context,
		)
		if err != nil {
			return err
		}
		var rowID int64
		if err := tx.QueryRow(`
			SELECT id FROM symbol_refs
			WHERE path = ? AND name = ? AND line = ?`,
			ref.Path,
			ref.Name,
			ref.Line,
		).Scan(&rowID); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO symbol_refs_fts(rowid, name, language, path, context)
			VALUES (?, ?, ?, ?, ?)`,
			rowID,
			ref.Name,
			ref.Language,
			ref.Path,
			ref.Context,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ExtractSymbols(language, path, content string) []Symbol {
	language = NormalizeLanguage(language)
	if language == "go" {
		if symbols, ok := extractGoSymbolsAST(path, content); ok {
			return symbols
		}
	}
	if language == "py" {
		return extractPythonSymbols(path, content)
	}
	patterns := symbolPatterns[language]
	if len(patterns) == 0 {
		return nil
	}

	var symbols []Symbol
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		for _, pattern := range patterns {
			match := pattern.re.FindStringSubmatch(line)
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(match[1])
			if name == "" || protectedIdentifier(name) {
				continue
			}
			symbols = append(symbols, Symbol{
				Name:      name,
				Kind:      pattern.kind,
				Signature: strings.TrimSpace(line),
				Language:  language,
				Path:      path,
				Line:      idx + 1,
			})
			break
		}
	}
	return symbols
}

type pythonClassScope struct {
	name   string
	indent int
}

func extractPythonSymbols(path, content string) []Symbol {
	var symbols []Symbol
	var classStack []pythonClassScope
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		trimmed := strings.TrimSpace(stripLineComment("py", line))
		if trimmed == "" {
			continue
		}

		indent := leadingIndentWidth(line)
		for len(classStack) > 0 && indent <= classStack[len(classStack)-1].indent {
			classStack = classStack[:len(classStack)-1]
		}

		if match := symbolPatterns["py"][0].re.FindStringSubmatch(line); len(match) > 1 {
			name := strings.TrimSpace(match[1])
			if name == "" || protectedIdentifier(name) {
				continue
			}
			symbols = append(symbols, Symbol{
				Name:      name,
				Kind:      "class",
				Signature: strings.TrimSpace(line),
				Language:  "py",
				Path:      path,
				Line:      idx + 1,
			})
			classStack = append(classStack, pythonClassScope{name: name, indent: indent})
			continue
		}

		if match := symbolPatterns["py"][1].re.FindStringSubmatch(line); len(match) > 1 {
			name := strings.TrimSpace(match[1])
			if name == "" || protectedIdentifier(name) {
				continue
			}
			kind := "function"
			signature := strings.TrimSpace(line)
			if len(classStack) > 0 {
				kind = "method"
				signature = pythonQualifiedName(classStack, name) + ": " + signature
			}
			symbols = append(symbols, Symbol{
				Name:      name,
				Kind:      kind,
				Signature: signature,
				Language:  "py",
				Path:      path,
				Line:      idx + 1,
			})
		}
	}
	return symbols
}

func leadingIndentWidth(line string) int {
	width := 0
	for _, char := range line {
		switch char {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}
	return width
}

func pythonQualifiedName(scopes []pythonClassScope, name string) string {
	parts := make([]string, 0, len(scopes)+1)
	for _, scope := range scopes {
		parts = append(parts, scope.name)
	}
	parts = append(parts, name)
	return strings.Join(parts, ".")
}

func ExtractSymbolReferences(language, path, content string) []SymbolReference {
	language = NormalizeLanguage(language)
	if !supportsSymbolReferences(language) {
		return nil
	}
	if language == "go" {
		if refs, ok := extractGoSymbolReferencesAST(path, content); ok {
			return refs
		}
	}

	definedOnLine := map[int]map[string]bool{}
	for _, symbol := range ExtractSymbols(language, path, content) {
		if definedOnLine[symbol.Line] == nil {
			definedOnLine[symbol.Line] = map[string]bool{}
		}
		definedOnLine[symbol.Line][symbol.Name] = true
	}

	seen := map[string]bool{}
	var refs []SymbolReference
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		lineNumber := idx + 1
		for _, name := range callNamesFromLine(language, line) {
			if definedOnLine[lineNumber][name] || protectedIdentifier(name) || commonSymbolReferenceName(name) {
				continue
			}
			key := fmt.Sprintf("%s:%d:%s", path, lineNumber, name)
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, SymbolReference{
				Name:     name,
				Language: language,
				Path:     path,
				Line:     lineNumber,
				Context:  strings.TrimSpace(line),
			})
		}
	}
	return refs
}

func SearchSymbols(db *sql.DB, query string, limit int) ([]Symbol, error) {
	limit = normalizeResultLimit(limit, 20)
	tokens := searchTokens(query)
	var matches []Symbol
	seen := map[int]bool{}

	if ftsQuery := metadataFTSQuery(tokens); ftsQuery != "" {
		rows, err := db.Query(`
			SELECT s.id, s.name, s.kind, s.signature, s.language, s.path, s.line
			FROM symbols s
			JOIN symbols_fts f ON s.id = f.rowid
			WHERE symbols_fts MATCH ?
			ORDER BY bm25(symbols_fts, 8.0, 1.0, 4.0, 1.0, 2.0)
			LIMIT ?`, ftsQuery, metadataFTSCandidateLimit(limit))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			symbol, err := scanSymbol(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			seen[symbol.ID] = true
			if symbolMatches(symbol, tokens) {
				matches = append(matches, symbol)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	if len(matches) < limit {
		sqlText := `
			SELECT id, name, kind, signature, language, path, line
			FROM symbols`
		args := []any(nil)
		if where, whereArgs := metadataSearchWhere(tokens, "name", "kind", "signature", "language", "path"); where != "" {
			sqlText += " WHERE " + where
			args = append(args, whereArgs...)
		}
		sqlText += " ORDER BY path ASC, line ASC"
		rows, err := db.Query(sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			symbol, err := scanSymbol(rows)
			if err != nil {
				return nil, err
			}
			if seen[symbol.ID] {
				continue
			}
			if symbolMatches(symbol, tokens) {
				matches = append(matches, symbol)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return symbolScore(matches[i], tokens) > symbolScore(matches[j], tokens)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func SearchSymbolReferences(db *sql.DB, query string, limit int) ([]SymbolReference, error) {
	limit = normalizeResultLimit(limit, 20)
	tokens := searchTokens(query)
	var matches []SymbolReference
	seen := map[int]bool{}

	if ftsQuery := metadataFTSQuery(tokens); ftsQuery != "" {
		rows, err := db.Query(`
			SELECT r.id, r.name, r.language, r.path, r.line, r.context
			FROM symbol_refs r
			JOIN symbol_refs_fts f ON r.id = f.rowid
			WHERE symbol_refs_fts MATCH ?
			ORDER BY bm25(symbol_refs_fts, 6.0, 1.0, 2.0, 4.0)
			LIMIT ?`, ftsQuery, metadataFTSCandidateLimit(limit))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			ref, err := scanSymbolReference(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			seen[ref.ID] = true
			if symbolReferenceMatches(ref, tokens) {
				matches = append(matches, ref)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	if len(matches) < limit {
		sqlText := `
			SELECT id, name, language, path, line, context
			FROM symbol_refs`
		args := []any(nil)
		if where, whereArgs := metadataSearchWhere(tokens, "name", "language", "path", "context"); where != "" {
			sqlText += " WHERE " + where
			args = append(args, whereArgs...)
		}
		sqlText += " ORDER BY path ASC, line ASC"
		rows, err := db.Query(sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			ref, err := scanSymbolReference(rows)
			if err != nil {
				return nil, err
			}
			if seen[ref.ID] {
				continue
			}
			if symbolReferenceMatches(ref, tokens) {
				matches = append(matches, ref)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return symbolReferenceScore(matches[i], tokens) > symbolReferenceScore(matches[j], tokens)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func BuildSymbolContext(db *sql.DB, query string, limit int) (SymbolContext, error) {
	limit = normalizeResultLimit(limit, 20)
	definitions, err := SearchSymbols(db, query, limit)
	if err != nil {
		return SymbolContext{}, err
	}
	references, err := SearchSymbolReferences(db, query, limit)
	if err != nil {
		return SymbolContext{}, err
	}
	return SymbolContext{
		Query:       strings.TrimSpace(query),
		Definitions: definitions,
		References:  references,
	}, nil
}

func RenderSymbolContext(context SymbolContext) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# SnapZip Symbol Context\n\nQuery: %s\n", context.Query)
	builder.WriteString("\n## Definitions\n")
	if len(context.Definitions) == 0 {
		builder.WriteString("\nNo matching definitions found.\n")
	} else {
		for _, symbol := range context.Definitions {
			fmt.Fprintf(&builder, "- %s:%d [%s %s] %s\n", symbol.Path, symbol.Line, symbol.Language, symbol.Kind, symbol.Signature)
		}
	}
	builder.WriteString("\n## References\n")
	if len(context.References) == 0 {
		builder.WriteString("\nNo matching references found.\n")
	} else {
		for _, ref := range context.References {
			fmt.Fprintf(&builder, "- %s:%d [%s] %s\n", ref.Path, ref.Line, ref.Language, ref.Context)
		}
	}
	return builder.String()
}

func BuildRepoMap(db *sql.DB, limit int) (RepoMap, error) {
	limit = normalizeResultLimit(limit, 100)
	rows, err := db.Query(`
		SELECT id, name, kind, signature, language, path, line
		FROM symbols
		ORDER BY path ASC, line ASC
		LIMIT ?`, limit*20)
	if err != nil {
		return RepoMap{}, err
	}
	defer rows.Close()

	files := make(map[string]*RepoMapFile)
	var order []string
	for rows.Next() {
		symbol, err := scanSymbol(rows)
		if err != nil {
			return RepoMap{}, err
		}
		file := files[symbol.Path]
		if file == nil {
			files[symbol.Path] = &RepoMapFile{Path: symbol.Path, Language: symbol.Language}
			file = files[symbol.Path]
			order = append(order, symbol.Path)
		}
		if len(file.Symbols) < 25 {
			file.Symbols = append(file.Symbols, symbol)
		}
	}
	if err := rows.Err(); err != nil {
		return RepoMap{}, err
	}

	if len(order) == 0 {
		return repoMapFromKnowledge(db, limit)
	}
	if len(order) > limit {
		order = order[:limit]
	}

	result := RepoMap{Files: make([]RepoMapFile, 0, len(order))}
	for _, path := range order {
		result.Files = append(result.Files, *files[path])
	}
	return result, nil
}

func RelatedFiles(db *sql.DB, path string, limit int) ([]RepoMapFile, error) {
	limit = normalizeResultLimit(limit, 10)
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}

	rows, err := db.Query(`
		SELECT id, name, kind, signature, language, path, line
		FROM symbols
		WHERE path = ?
		ORDER BY line ASC`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		symbol, err := scanSymbol(rows)
		if err != nil {
			return nil, err
		}
		names = append(names, symbol.Name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	scoreByPath := map[string]int{}
	languageByPath := map[string]string{}
	symbolsByPath := map[string][]Symbol{}
	for _, name := range names {
		found, err := SearchSymbols(db, name, 100)
		if err != nil {
			return nil, err
		}
		for _, symbol := range found {
			if symbol.Path == path {
				continue
			}
			scoreByPath[symbol.Path]++
			languageByPath[symbol.Path] = symbol.Language
			if len(symbolsByPath[symbol.Path]) < 10 {
				symbolsByPath[symbol.Path] = append(symbolsByPath[symbol.Path], symbol)
			}
		}
		refs, err := SearchSymbolReferences(db, name, 100)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if ref.Path == path {
				continue
			}
			scoreByPath[ref.Path] += 2
			languageByPath[ref.Path] = ref.Language
		}
	}
	if err := scoreImportRelatedFiles(db, path, scoreByPath, languageByPath); err != nil {
		return nil, err
	}
	if len(scoreByPath) == 0 {
		return relatedFilesByPathTokens(db, path, limit)
	}

	type scoredPath struct {
		path  string
		score int
	}
	var scored []scoredPath
	for candidate, score := range scoreByPath {
		scored = append(scored, scoredPath{path: candidate, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].path < scored[j].path
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}

	files := make([]RepoMapFile, 0, len(scored))
	for _, item := range scored {
		files = append(files, RepoMapFile{
			Path:     item.path,
			Language: languageByPath[item.path],
			Symbols:  symbolsByPath[item.path],
		})
	}
	return files, nil
}

func RenderRepoMap(repoMap RepoMap) string {
	var builder strings.Builder
	builder.WriteString("# SnapZip Repo Map\n")
	for _, file := range repoMap.Files {
		fmt.Fprintf(&builder, "\n## %s", file.Path)
		if file.Language != "" {
			fmt.Fprintf(&builder, " (%s)", file.Language)
		}
		builder.WriteByte('\n')
		if len(file.Symbols) == 0 {
			builder.WriteString("- No indexed symbols\n")
			continue
		}
		for _, symbol := range file.Symbols {
			fmt.Fprintf(&builder, "- L%d %s %s", symbol.Line, symbol.Kind, symbol.Name)
			if symbol.Signature != "" {
				fmt.Fprintf(&builder, ": `%s`", symbol.Signature)
			}
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func scanSymbol(rows interface {
	Scan(dest ...any) error
}) (Symbol, error) {
	var symbol Symbol
	err := rows.Scan(&symbol.ID, &symbol.Name, &symbol.Kind, &symbol.Signature, &symbol.Language, &symbol.Path, &symbol.Line)
	return symbol, err
}

func scanSymbolReference(rows interface {
	Scan(dest ...any) error
}) (SymbolReference, error) {
	var ref SymbolReference
	err := rows.Scan(&ref.ID, &ref.Name, &ref.Language, &ref.Path, &ref.Line, &ref.Context)
	return ref, err
}

func symbolMatches(symbol Symbol, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	haystack := strings.ToLower(symbol.Name + " " + symbol.Kind + " " + symbol.Signature + " " + symbol.Language + " " + symbol.Path)
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			return true
		}
	}
	return false
}

func symbolScore(symbol Symbol, tokens []string) int {
	score := 0
	lowerName := strings.ToLower(symbol.Name)
	lowerPath := strings.ToLower(symbol.Path)
	lowerSignature := strings.ToLower(symbol.Signature)
	for _, token := range tokens {
		switch {
		case lowerName == token:
			score += 10
		case strings.Contains(lowerName, token):
			score += 6
		case strings.Contains(lowerPath, token):
			score += 3
		case strings.Contains(lowerSignature, token):
			score += 2
		}
	}
	return score
}

func supportsSymbolReferences(language string) bool {
	switch NormalizeLanguage(language) {
	case "go", "py", "rb", "js", "jsx", "ts", "tsx", "java", "rs", "php", "swift", "kt":
		return true
	default:
		return false
	}
}

func callNamesFromLine(language, line string) []string {
	language = NormalizeLanguage(language)
	line = stripLineComment(language, line)
	var names []string
	for _, match := range symbolReferenceCallPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			names = append(names, match[1])
		}
	}
	return uniqueStrings(names)
}

func stripLineComment(language, line string) string {
	switch NormalizeLanguage(language) {
	case "py", "rb":
		if idx := strings.Index(line, "#"); idx >= 0 {
			return line[:idx]
		}
	case "css", "scss", "sass", "less", "html", "vue", "svelte", "astro":
		return line
	default:
		if idx := strings.Index(line, "//"); idx >= 0 {
			return line[:idx]
		}
	}
	return line
}

func commonSymbolReferenceName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "append", "array", "assert", "assertequal", "assertfalse", "assertin", "assertis", "assertisnone", "assertisnotnone", "assertraises", "asserttrue", "bool", "boolean", "cap", "copy", "delete", "dict", "format", "int", "len", "list", "log", "make", "object", "open", "print", "printf", "println", "set", "str", "string", "super", "tuple":
		return true
	default:
		return commonFailureWord(name)
	}
}

func symbolReferenceMatches(ref SymbolReference, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	haystack := strings.ToLower(ref.Name + " " + ref.Language + " " + ref.Path + " " + ref.Context)
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			return true
		}
	}
	return false
}

func symbolReferenceScore(ref SymbolReference, tokens []string) int {
	score := 0
	lowerName := strings.ToLower(ref.Name)
	lowerPath := strings.ToLower(ref.Path)
	lowerContext := strings.ToLower(ref.Context)
	for _, token := range tokens {
		switch {
		case lowerName == token:
			score += 8
		case strings.Contains(lowerName, token):
			score += 5
		case strings.Contains(lowerPath, token):
			score += 3
		case strings.Contains(lowerContext, token):
			score += 2
		}
	}
	return score
}

func repoMapFromKnowledge(db *sql.DB, limit int) (RepoMap, error) {
	rows, err := db.Query(`
		SELECT language, path
		FROM knowledge
		WHERE path != ''
		GROUP BY path, language
		ORDER BY path ASC
		LIMIT ?`, limit)
	if err != nil {
		return RepoMap{}, err
	}
	defer rows.Close()

	var repoMap RepoMap
	for rows.Next() {
		var file RepoMapFile
		if err := rows.Scan(&file.Language, &file.Path); err != nil {
			return RepoMap{}, err
		}
		repoMap.Files = append(repoMap.Files, file)
	}
	return repoMap, rows.Err()
}

func relatedFilesByPathTokens(db *sql.DB, path string, limit int) ([]RepoMapFile, error) {
	tokens := strings.FieldsFunc(strings.ToLower(path), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	rows, err := db.Query(`
		SELECT language, path
		FROM knowledge
		WHERE path != '' AND path != ?
		GROUP BY path, language
		ORDER BY path ASC`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []RepoMapFile
	for rows.Next() {
		var file RepoMapFile
		if err := rows.Scan(&file.Language, &file.Path); err != nil {
			return nil, err
		}
		lowerPath := strings.ToLower(file.Path)
		for _, token := range tokens {
			if len(token) > 2 && strings.Contains(lowerPath, token) {
				files = append(files, file)
				break
			}
		}
		if len(files) >= limit {
			break
		}
	}
	return files, rows.Err()
}
