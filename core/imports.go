package core

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type ImportReference struct {
	ID         int    `json:"id,omitempty"`
	ImportPath string `json:"import_path"`
	Alias      string `json:"alias,omitempty"`
	Language   string `json:"language"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Context    string `json:"context,omitempty"`
}

type ImportContext struct {
	Query   string            `json:"query"`
	Imports []ImportReference `json:"imports"`
}

var (
	goSingleImportPattern  = regexp.MustCompile("^\\s*import\\s+(?:([A-Za-z_][A-Za-z0-9_]*|\\.|_)\\s+)?[\"`]([^\"`]+)[\"`]")
	goBlockImportPattern   = regexp.MustCompile("^\\s*(?:([A-Za-z_][A-Za-z0-9_]*|\\.|_)\\s+)?[\"`]([^\"`]+)[\"`]")
	pythonImportPattern    = regexp.MustCompile(`^\s*import\s+(.+)`)
	pythonFromPattern      = regexp.MustCompile(`^\s*from\s+([.]*[A-Za-z_][A-Za-z0-9_\.]*)\s+import\s+(.+)`)
	rubyRequirePattern     = regexp.MustCompile(`^\s*(?:require|require_relative|load)\s+["']([^"']+)["']`)
	jsImportPattern        = regexp.MustCompile(`^\s*import\s+(?:(.*?)\s+from\s+)?["']([^"']+)["']`)
	jsRequirePattern       = regexp.MustCompile(`(?:const|let|var)?\s*([^=;\n]*)=?\s*require\(\s*["']([^"']+)["']\s*\)`)
	jsExportFromPattern    = regexp.MustCompile(`^\s*export\s+.*\s+from\s+["']([^"']+)["']`)
	javaImportPattern      = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([^;]+);?`)
	csharpUsingPattern     = regexp.MustCompile(`^\s*using\s+(?:(\w+)\s*=\s*)?([^;]+);?`)
	rustUsePattern         = regexp.MustCompile(`^\s*use\s+([^;]+);?`)
	rustExternPattern      = regexp.MustCompile(`^\s*extern\s+crate\s+([A-Za-z_][A-Za-z0-9_]*);?`)
	swiftImportPattern     = regexp.MustCompile(`^\s*(?:@testable\s+)?import\s+([A-Za-z_][A-Za-z0-9_\.]*)`)
	phpUsePattern          = regexp.MustCompile(`^\s*use\s+([^;]+);?`)
	phpRequirePattern      = regexp.MustCompile(`^\s*(?:require|require_once|include|include_once)\s*(?:\(?\s*)["']([^"']+)["']`)
	cssImportPattern       = regexp.MustCompile(`@import\s+(?:url\(\s*)?["']?([^"')\s;]+)["']?`)
	htmlScriptSrcPattern   = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+)["']`)
	htmlLinkHrefPattern    = regexp.MustCompile(`(?i)<link[^>]+href=["']([^"']+)["']`)
	htmlAssetImportPattern = regexp.MustCompile(`(?i)<(?:img|source|iframe)[^>]+src=["']([^"']+)["']`)
)

func ReplaceImportsForFile(db *sql.DB, language, path string, content []byte) error {
	language = NormalizeLanguage(language)
	path = strings.TrimSpace(path)
	refs := ExtractImports(language, path, string(content))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM import_refs WHERE path = ?", path); err != nil {
		return err
	}
	for _, ref := range refs {
		_, err := tx.Exec(`
			INSERT INTO import_refs (import_path, alias, language, path, line, context)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(path, import_path, line) DO UPDATE SET
				alias = excluded.alias,
				language = excluded.language,
				context = excluded.context,
				created_at = CURRENT_TIMESTAMP`,
			ref.ImportPath,
			ref.Alias,
			ref.Language,
			ref.Path,
			ref.Line,
			ref.Context,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ExtractImports(language, path, content string) []ImportReference {
	language = NormalizeLanguage(language)
	if !supportsImportReferences(language) {
		return nil
	}

	seen := map[string]bool{}
	var refs []ImportReference
	inGoImportBlock := false
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		lineNumber := idx + 1
		context := strings.TrimSpace(line)
		if context == "" {
			continue
		}
		trimmed := strings.TrimSpace(stripLineComment(language, line))
		if trimmed == "" {
			continue
		}

		switch language {
		case "go":
			if strings.HasPrefix(trimmed, "import (") {
				inGoImportBlock = true
				continue
			}
			if inGoImportBlock {
				if strings.HasPrefix(trimmed, ")") {
					inGoImportBlock = false
					continue
				}
				appendImportMatch(&refs, seen, language, path, lineNumber, context, goBlockImportPattern.FindStringSubmatch(trimmed), 2, 1)
				continue
			}
			appendImportMatch(&refs, seen, language, path, lineNumber, context, goSingleImportPattern.FindStringSubmatch(trimmed), 2, 1)
		case "py":
			appendPythonImports(&refs, seen, language, path, lineNumber, context, trimmed)
		case "rb":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, rubyRequirePattern.FindStringSubmatch(trimmed), 1, 0)
		case "js", "jsx", "ts", "tsx", "mjs", "cjs":
			appendJSImport(&refs, seen, language, path, lineNumber, context, trimmed)
		case "java", "kt", "kts":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, javaImportPattern.FindStringSubmatch(trimmed), 1, 0)
		case "rs":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, rustUsePattern.FindStringSubmatch(trimmed), 1, 0)
			appendImportMatch(&refs, seen, language, path, lineNumber, context, rustExternPattern.FindStringSubmatch(trimmed), 1, 0)
		case "php":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, phpUsePattern.FindStringSubmatch(trimmed), 1, 0)
			appendImportMatch(&refs, seen, language, path, lineNumber, context, phpRequirePattern.FindStringSubmatch(trimmed), 1, 0)
		case "swift":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, swiftImportPattern.FindStringSubmatch(trimmed), 1, 0)
		case "cs":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, csharpUsingPattern.FindStringSubmatch(trimmed), 2, 1)
		case "css", "scss", "sass", "less":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, cssImportPattern.FindStringSubmatch(trimmed), 1, 0)
		case "html", "vue", "svelte", "astro":
			appendImportMatch(&refs, seen, language, path, lineNumber, context, htmlScriptSrcPattern.FindStringSubmatch(trimmed), 1, 0)
			appendImportMatch(&refs, seen, language, path, lineNumber, context, htmlLinkHrefPattern.FindStringSubmatch(trimmed), 1, 0)
			appendImportMatch(&refs, seen, language, path, lineNumber, context, htmlAssetImportPattern.FindStringSubmatch(trimmed), 1, 0)
		}
	}
	return refs
}

func SearchImports(db *sql.DB, query string, limit int) ([]ImportReference, error) {
	limit = normalizeResultLimit(limit, 20)
	tokens := searchTokens(query)
	rows, err := db.Query(`
		SELECT id, import_path, alias, language, path, line, context
		FROM import_refs
		ORDER BY path ASC, line ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []ImportReference
	for rows.Next() {
		ref, err := scanImportReference(rows)
		if err != nil {
			return nil, err
		}
		if importReferenceMatches(ref, tokens) {
			matches = append(matches, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return importReferenceScore(matches[i], tokens) > importReferenceScore(matches[j], tokens)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func BuildImportContext(db *sql.DB, query string, limit int) (ImportContext, error) {
	imports, err := SearchImports(db, query, limit)
	if err != nil {
		return ImportContext{}, err
	}
	return ImportContext{
		Query:   strings.TrimSpace(query),
		Imports: imports,
	}, nil
}

func RenderImportContext(context ImportContext) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# SnapZip Import Context\n\nQuery: %s\n", context.Query)
	builder.WriteString("\n## Imports\n")
	if len(context.Imports) == 0 {
		builder.WriteString("\nNo matching imports found.\n")
		return builder.String()
	}
	for _, ref := range context.Imports {
		alias := ""
		if ref.Alias != "" {
			alias = " as " + ref.Alias
		}
		fmt.Fprintf(&builder, "- %s:%d [%s] %s%s", ref.Path, ref.Line, ref.Language, ref.ImportPath, alias)
		if ref.Context != "" {
			fmt.Fprintf(&builder, " | %s", ref.Context)
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func scoreImportRelatedFiles(db *sql.DB, sourcePath string, scoreByPath map[string]int, languageByPath map[string]string) error {
	for _, query := range importQueriesForPath(sourcePath) {
		refs, err := SearchImports(db, query, 100)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.Path == sourcePath {
				continue
			}
			if !importPathMatchesQuery(ref.ImportPath, query) {
				continue
			}
			scoreByPath[ref.Path] += 3 + importQuerySpecificityBonus(query)
			languageByPath[ref.Path] = ref.Language
		}
	}

	declared, err := importsForPath(db, sourcePath)
	if err != nil {
		return err
	}
	for _, sourceImport := range declared {
		if !localImportCandidate(sourceImport.ImportPath) {
			continue
		}
		refs, err := SearchImports(db, sourceImport.ImportPath, 100)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.Path == sourcePath {
				continue
			}
			if !importPathMatchesQuery(ref.ImportPath, sourceImport.ImportPath) {
				continue
			}
			scoreByPath[ref.Path] += 2
			languageByPath[ref.Path] = ref.Language
		}
	}
	return nil
}

func importsForPath(db *sql.DB, path string) ([]ImportReference, error) {
	rows, err := db.Query(`
		SELECT id, import_path, alias, language, path, line, context
		FROM import_refs
		WHERE path = ?
		ORDER BY line ASC`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []ImportReference
	for rows.Next() {
		ref, err := scanImportReference(rows)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func appendPythonImports(refs *[]ImportReference, seen map[string]bool, language, path string, line int, context, trimmed string) {
	if match := pythonFromPattern.FindStringSubmatch(trimmed); len(match) > 2 {
		appendImport(refs, seen, language, path, line, context, match[1], importedPythonName(match[2]))
		return
	}
	match := pythonImportPattern.FindStringSubmatch(trimmed)
	if len(match) < 2 {
		return
	}
	for _, part := range strings.Split(match[1], ",") {
		module, alias := splitAlias(part, " as ")
		appendImport(refs, seen, language, path, line, context, module, alias)
	}
}

func appendJSImport(refs *[]ImportReference, seen map[string]bool, language, path string, line int, context, trimmed string) {
	if match := jsImportPattern.FindStringSubmatch(trimmed); len(match) > 2 {
		appendImport(refs, seen, language, path, line, context, match[2], cleanImportAlias(match[1]))
		return
	}
	if match := jsExportFromPattern.FindStringSubmatch(trimmed); len(match) > 1 {
		appendImport(refs, seen, language, path, line, context, match[1], "")
		return
	}
	if match := jsRequirePattern.FindStringSubmatch(trimmed); len(match) > 2 {
		appendImport(refs, seen, language, path, line, context, match[2], cleanImportAlias(match[1]))
	}
}

func appendImportMatch(refs *[]ImportReference, seen map[string]bool, language, path string, line int, context string, match []string, importIndex, aliasIndex int) {
	if len(match) <= importIndex {
		return
	}
	alias := ""
	if aliasIndex > 0 && len(match) > aliasIndex {
		alias = match[aliasIndex]
	}
	appendImport(refs, seen, language, path, line, context, match[importIndex], alias)
}

func appendImport(refs *[]ImportReference, seen map[string]bool, language, path string, line int, context, importPath, alias string) {
	importPath = cleanImportPath(importPath)
	alias = cleanImportAlias(alias)
	if importPath == "" {
		return
	}
	key := fmt.Sprintf("%s:%d:%s", path, line, strings.ToLower(importPath))
	if seen[key] {
		return
	}
	seen[key] = true
	*refs = append(*refs, ImportReference{
		ImportPath: importPath,
		Alias:      alias,
		Language:   language,
		Path:       path,
		Line:       line,
		Context:    context,
	})
}

func scanImportReference(rows interface {
	Scan(dest ...any) error
}) (ImportReference, error) {
	var ref ImportReference
	err := rows.Scan(&ref.ID, &ref.ImportPath, &ref.Alias, &ref.Language, &ref.Path, &ref.Line, &ref.Context)
	return ref, err
}

func supportsImportReferences(language string) bool {
	switch NormalizeLanguage(language) {
	case "go", "py", "rb", "js", "jsx", "ts", "tsx", "mjs", "cjs", "java", "kt", "kts", "rs", "php", "swift", "cs", "css", "scss", "sass", "less", "html", "vue", "svelte", "astro":
		return true
	default:
		return false
	}
}

func importReferenceMatches(ref ImportReference, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	haystack := strings.ToLower(ref.ImportPath + " " + ref.Alias + " " + ref.Language + " " + ref.Path + " " + ref.Context)
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			return true
		}
	}
	return false
}

func importReferenceScore(ref ImportReference, tokens []string) int {
	score := 0
	lowerImport := strings.ToLower(ref.ImportPath)
	lowerAlias := strings.ToLower(ref.Alias)
	lowerPath := strings.ToLower(ref.Path)
	lowerContext := strings.ToLower(ref.Context)
	for _, token := range tokens {
		switch {
		case lowerImport == token:
			score += 12
		case importPathBase(lowerImport) == token:
			score += 9
		case strings.Contains(lowerImport, token):
			score += 8
		case lowerAlias == token:
			score += 6
		case strings.Contains(lowerAlias, token):
			score += 4
		case strings.Contains(lowerPath, token):
			score += 3
		case strings.Contains(lowerContext, token):
			score += 2
		}
	}
	return score
}

func importQueriesForPath(path string) []string {
	normalized := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(path)), "./")
	ext := filepath.Ext(normalized)
	noExt := strings.TrimSuffix(normalized, ext)
	if noExt == "" {
		return nil
	}
	parts := strings.Split(noExt, "/")
	var queries []string
	queries = append(queries, noExt, strings.Join(parts, "."))
	if len(parts) > 1 {
		queries = append(queries, strings.Join(parts[1:], "/"), strings.Join(parts[1:], "."))
	}
	base := filepath.Base(noExt)
	queries = append(queries, base)
	if strings.HasSuffix(base, "_test") || strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") {
		queries = append(queries, strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(base, "_test"), ".test"), ".spec"))
	}

	var filtered []string
	for _, query := range uniqueStrings(queries) {
		query = strings.Trim(query, "./")
		if len(query) > 2 {
			filtered = append(filtered, query)
		}
	}
	return filtered
}

func importQuerySpecificityBonus(query string) int {
	if strings.ContainsAny(query, "./\\") {
		return 3
	}
	return 0
}

func importPathMatchesQuery(importPath, query string) bool {
	importPath = strings.ToLower(strings.TrimSpace(importPath))
	query = strings.ToLower(strings.TrimSpace(strings.Trim(query, "./\\")))
	if importPath == "" || query == "" {
		return false
	}

	slashImport := normalizeImportSeparators(importPath, "/")
	dotImport := normalizeImportSeparators(importPath, ".")
	slashQuery := normalizeImportSeparators(query, "/")
	dotQuery := normalizeImportSeparators(query, ".")
	if strings.Contains(slashImport, slashQuery) || strings.Contains(dotImport, dotQuery) {
		return true
	}
	if !strings.ContainsAny(query, "./\\:") {
		return importPathBase(importPath) == query
	}
	return false
}

func localImportCandidate(importPath string) bool {
	value := strings.TrimSpace(importPath)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, ".") {
		return true
	}
	return strings.Contains(value, "/") || strings.Contains(value, "\\") || strings.Count(value, ".") >= 1
}

func cleanImportPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`+"`")
	value = strings.TrimSuffix(value, ";")
	value = strings.TrimSuffix(value, ",")
	value = strings.TrimSpace(value)
	if idx := strings.Index(value, " as "); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if idx := strings.Index(value, "{"); idx >= 0 {
		value = strings.TrimRight(strings.TrimSpace(value[:idx]), ":./\\")
	}
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`+"`")
	return value
}

func cleanImportAlias(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "{}*")
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, " as "); idx >= 0 {
		value = strings.TrimSpace(value[idx+4:])
	}
	if strings.Contains(value, ",") {
		value = strings.TrimSpace(strings.Split(value, ",")[0])
	}
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}

func splitAlias(value, marker string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(value), marker, 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return value, ""
}

func importedPythonName(value string) string {
	first := strings.TrimSpace(strings.Split(value, ",")[0])
	name, alias := splitAlias(first, " as ")
	if alias != "" {
		return alias
	}
	return name
}

func importPathBase(value string) string {
	value = strings.Trim(value, "./\\")
	value = strings.ReplaceAll(value, "::", "/")
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.ReplaceAll(value, ".", "/")
	return filepath.Base(value)
}

func normalizeImportSeparators(value, separator string) string {
	value = strings.Trim(value, "./\\")
	replacer := strings.NewReplacer("::", separator, "\\", separator, "/", separator, ".", separator)
	return replacer.Replace(value)
}
