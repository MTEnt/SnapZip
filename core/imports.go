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
	TargetPath string `json:"target_path,omitempty"`
	Line       int    `json:"line"`
	Context    string `json:"context,omitempty"`
}

type ImportContext struct {
	Query   string            `json:"query"`
	Imports []ImportReference `json:"imports"`
}

type DependencyGraph struct {
	Path       string            `json:"path"`
	Language   string            `json:"language,omitempty"`
	Imports    []ImportReference `json:"imports,omitempty"`
	ImportedBy []ImportReference `json:"imported_by,omitempty"`
}

type indexedImportFile struct {
	Path     string
	Language string
}

type importResolutionIndex struct {
	Files  []indexedImportFile
	ByPath map[string]indexedImportFile
	ByDir  map[string][]indexedImportFile
}

var (
	goSingleImportPattern  = regexp.MustCompile("^\\s*import\\s+(?:([A-Za-z_][A-Za-z0-9_]*|\\.|_)\\s+)?[\"`]([^\"`]+)[\"`]")
	goBlockImportPattern   = regexp.MustCompile("^\\s*(?:([A-Za-z_][A-Za-z0-9_]*|\\.|_)\\s+)?[\"`]([^\"`]+)[\"`]")
	pythonImportPattern    = regexp.MustCompile(`^\s*import\s+(.+)`)
	pythonFromPattern      = regexp.MustCompile(`^\s*from\s+([.]*[A-Za-z_][A-Za-z0-9_\.]*)\s+import\s+(.+)`)
	rubyRequirePattern     = regexp.MustCompile(`^\s*(require|require_relative|load)\s+["']([^"']+)["']`)
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
			INSERT INTO import_refs (import_path, alias, language, path, target_path, line, context)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path, import_path, line) DO UPDATE SET
				alias = excluded.alias,
				language = excluded.language,
				target_path = excluded.target_path,
				context = excluded.context,
				created_at = CURRENT_TIMESTAMP`,
			ref.ImportPath,
			ref.Alias,
			ref.Language,
			ref.Path,
			ref.TargetPath,
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
			appendRubyImport(&refs, seen, language, path, lineNumber, context, trimmed)
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
		SELECT id, import_path, alias, language, path, target_path, line, context
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

func BuildDependencyGraph(db *sql.DB, path string, limit int) (DependencyGraph, error) {
	path = normalizeIndexedPath(path)
	limit = normalizeResultLimit(limit, 20)

	languageByPath, err := indexedPathLanguages(db)
	if err != nil {
		return DependencyGraph{}, err
	}
	language := languageByPath[path]
	if language == "" {
		language = LanguageFromPath(path)
	}

	outgoing, err := importsForPath(db, path)
	if err != nil {
		return DependencyGraph{}, err
	}
	incoming, err := importsTargetingPath(db, path)
	if err != nil {
		return DependencyGraph{}, err
	}

	return DependencyGraph{
		Path:       path,
		Language:   language,
		Imports:    limitImportReferences(outgoing, limit),
		ImportedBy: limitImportReferences(incoming, limit),
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
		if ref.TargetPath != "" {
			fmt.Fprintf(&builder, " -> %s", ref.TargetPath)
		}
		if ref.Context != "" {
			fmt.Fprintf(&builder, " | %s", ref.Context)
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func RenderDependencyGraph(graph DependencyGraph) string {
	var builder strings.Builder
	builder.WriteString("# SnapZip Dependency Graph\n\n")
	fmt.Fprintf(&builder, "Path: %s", graph.Path)
	if graph.Language != "" {
		fmt.Fprintf(&builder, " [%s]", graph.Language)
	}
	builder.WriteByte('\n')

	builder.WriteString("\n## Imports\n")
	if len(graph.Imports) == 0 {
		builder.WriteString("\nNo indexed outgoing imports found.\n")
	} else {
		for _, ref := range graph.Imports {
			renderGraphImport(&builder, ref, false)
		}
	}

	builder.WriteString("\n## Imported By\n")
	if len(graph.ImportedBy) == 0 {
		builder.WriteString("\nNo indexed incoming local imports found.\n")
	} else {
		for _, ref := range graph.ImportedBy {
			renderGraphImport(&builder, ref, true)
		}
	}
	return builder.String()
}

func renderGraphImport(builder *strings.Builder, ref ImportReference, includeSource bool) {
	if includeSource {
		fmt.Fprintf(builder, "- %s:%d [%s] %s", ref.Path, ref.Line, ref.Language, ref.ImportPath)
	} else {
		fmt.Fprintf(builder, "- L%d [%s] %s", ref.Line, ref.Language, ref.ImportPath)
	}
	if ref.Alias != "" {
		fmt.Fprintf(builder, " as %s", ref.Alias)
	}
	if ref.TargetPath != "" {
		fmt.Fprintf(builder, " -> %s", ref.TargetPath)
	} else {
		builder.WriteString(" (unresolved)")
	}
	if ref.Context != "" {
		fmt.Fprintf(builder, " | %s", ref.Context)
	}
	builder.WriteByte('\n')
}

func limitImportReferences(refs []ImportReference, limit int) []ImportReference {
	if len(refs) > limit {
		return refs[:limit]
	}
	return refs
}

func scoreImportRelatedFiles(db *sql.DB, sourcePath string, scoreByPath map[string]int, languageByPath map[string]string) error {
	if err := scoreResolvedImportRelatedFiles(db, sourcePath, scoreByPath, languageByPath); err != nil {
		return err
	}

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
		SELECT id, import_path, alias, language, path, target_path, line, context
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

func importsTargetingPath(db *sql.DB, path string) ([]ImportReference, error) {
	rows, err := db.Query(`
		SELECT id, import_path, alias, language, path, target_path, line, context
		FROM import_refs
		WHERE target_path = ?
		ORDER BY path ASC, line ASC`, path)
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

func scoreResolvedImportRelatedFiles(db *sql.DB, sourcePath string, scoreByPath map[string]int, languageByPath map[string]string) error {
	indexed, err := indexedPathLanguages(db)
	if err != nil {
		return err
	}
	outgoing, err := importsForPath(db, sourcePath)
	if err != nil {
		return err
	}
	for _, ref := range outgoing {
		if ref.TargetPath == "" || ref.TargetPath == sourcePath {
			continue
		}
		scoreByPath[ref.TargetPath] += 9
		languageByPath[ref.TargetPath] = indexed[ref.TargetPath]
	}

	incoming, err := importsTargetingPath(db, sourcePath)
	if err != nil {
		return err
	}
	for _, ref := range incoming {
		if ref.Path == "" || ref.Path == sourcePath {
			continue
		}
		scoreByPath[ref.Path] += 10
		languageByPath[ref.Path] = ref.Language
	}
	return nil
}

func importReceiptDetails(db *sql.DB, path string) ([]string, []string) {
	path = strings.TrimSpace(path)
	if db == nil || path == "" {
		return nil, nil
	}
	var reasons []string
	var evidence []string

	if outgoing, err := importsForPath(db, path); err == nil {
		for _, ref := range outgoing {
			if ref.TargetPath == "" {
				continue
			}
			reasons = append(reasons, "has resolved local import/dependency edges")
			evidence = append(evidence, fmt.Sprintf("imports %s -> %s", ref.ImportPath, ref.TargetPath))
			if len(evidence) >= 3 {
				return uniqueStrings(reasons), uniqueStrings(evidence)
			}
		}
	}
	if incoming, err := importsTargetingPath(db, path); err == nil {
		for _, ref := range incoming {
			if ref.Path == "" {
				continue
			}
			reasons = append(reasons, "is targeted by resolved local imports")
			evidence = append(evidence, fmt.Sprintf("%s imports %s", ref.Path, ref.ImportPath))
			if len(evidence) >= 3 {
				return uniqueStrings(reasons), uniqueStrings(evidence)
			}
		}
	}
	return uniqueStrings(reasons), uniqueStrings(evidence)
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

func appendRubyImport(refs *[]ImportReference, seen map[string]bool, language, path string, line int, context, trimmed string) {
	match := rubyRequirePattern.FindStringSubmatch(trimmed)
	if len(match) < 3 {
		return
	}
	importPath := match[2]
	if match[1] == "require_relative" && !strings.HasPrefix(importPath, ".") {
		importPath = "./" + importPath
	}
	appendImport(refs, seen, language, path, line, context, importPath, "")
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
	err := rows.Scan(&ref.ID, &ref.ImportPath, &ref.Alias, &ref.Language, &ref.Path, &ref.TargetPath, &ref.Line, &ref.Context)
	return ref, err
}

func ResolveImportTargets(db *sql.DB) error {
	index, err := buildImportResolutionIndex(db)
	if err != nil {
		return err
	}
	rows, err := db.Query(`
		SELECT id, import_path, alias, language, path, target_path, line, context
		FROM import_refs
		ORDER BY path ASC, line ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var refs []ImportReference
	for rows.Next() {
		ref, err := scanImportReference(rows)
		if err != nil {
			return err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, ref := range refs {
		target := resolveImportTarget(ref, index)
		if _, err := tx.Exec("UPDATE import_refs SET target_path = ? WHERE id = ?", target, ref.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	haystack := strings.ToLower(ref.ImportPath + " " + ref.Alias + " " + ref.Language + " " + ref.Path + " " + ref.TargetPath + " " + ref.Context)
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
	lowerTargetPath := strings.ToLower(ref.TargetPath)
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
		case lowerTargetPath == token:
			score += 7
		case strings.Contains(lowerTargetPath, token):
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

func buildImportResolutionIndex(db *sql.DB) (importResolutionIndex, error) {
	rows, err := db.Query(`
		SELECT path, language
		FROM knowledge
		WHERE path != ''
		GROUP BY path, language
		ORDER BY path ASC`)
	if err != nil {
		return importResolutionIndex{}, err
	}
	defer rows.Close()

	index := importResolutionIndex{
		ByPath: map[string]indexedImportFile{},
		ByDir:  map[string][]indexedImportFile{},
	}
	for rows.Next() {
		var file indexedImportFile
		if err := rows.Scan(&file.Path, &file.Language); err != nil {
			return importResolutionIndex{}, err
		}
		file.Path = normalizeIndexedPath(file.Path)
		file.Language = NormalizeLanguage(file.Language)
		if file.Path == "" {
			continue
		}
		index.Files = append(index.Files, file)
		index.ByPath[file.Path] = file
		dir := normalizeIndexedPath(filepath.Dir(file.Path))
		index.ByDir[dir] = append(index.ByDir[dir], file)
	}
	if err := rows.Err(); err != nil {
		return importResolutionIndex{}, err
	}
	for dir := range index.ByDir {
		sort.SliceStable(index.ByDir[dir], func(i, j int) bool {
			return importTargetLess(index.ByDir[dir][i], index.ByDir[dir][j])
		})
	}
	return index, nil
}

func resolveImportTarget(ref ImportReference, index importResolutionIndex) string {
	if !shouldResolveImport(ref) {
		return ""
	}
	for _, candidate := range importTargetCandidates(ref) {
		if target := selectImportTarget(ref, candidate, index); target != "" {
			return target
		}
	}
	return ""
}

func selectImportTarget(ref ImportReference, candidate string, index importResolutionIndex) string {
	candidate = normalizeIndexedPath(candidate)
	if candidate == "" {
		return ""
	}
	if file, ok := index.ByPath[candidate]; ok && file.Path != ref.Path {
		return file.Path
	}
	if target := selectImportTargetFromFiles(ref.Path, index.ByDir[candidate]); target != "" {
		return target
	}

	var exactMatches []indexedImportFile
	var dirMatches []indexedImportFile
	for _, file := range index.Files {
		if file.Path == ref.Path {
			continue
		}
		if pathSuffixMatch(file.Path, candidate) {
			exactMatches = append(exactMatches, file)
			continue
		}
		dir := normalizeIndexedPath(filepath.Dir(file.Path))
		if dir == candidate || strings.HasSuffix(dir, "/"+candidate) {
			dirMatches = append(dirMatches, file)
		}
	}
	if target := selectImportTargetFromFiles(ref.Path, exactMatches); target != "" {
		return target
	}
	return selectImportTargetFromFiles(ref.Path, dirMatches)
}

func selectImportTargetFromFiles(sourcePath string, files []indexedImportFile) string {
	if len(files) == 0 {
		return ""
	}
	sort.SliceStable(files, func(i, j int) bool {
		return importTargetLess(files[i], files[j])
	})
	for _, file := range files {
		if file.Path != sourcePath {
			return file.Path
		}
	}
	return ""
}

func importTargetLess(left, right indexedImportFile) bool {
	leftTest := isTestPath(left.Path)
	rightTest := isTestPath(right.Path)
	if leftTest != rightTest {
		return !leftTest
	}
	if len(left.Path) != len(right.Path) {
		return len(left.Path) < len(right.Path)
	}
	return left.Path < right.Path
}

func importTargetCandidates(ref ImportReference) []string {
	language := NormalizeLanguage(ref.Language)
	importPath := strings.TrimSpace(ref.ImportPath)
	if importPath == "" {
		return nil
	}

	var candidates []string
	addBase := func(base string, extensions []string, indexNames []string) {
		base = normalizeIndexedPath(base)
		if base == "" {
			return
		}
		candidates = append(candidates, base)
		if filepath.Ext(base) == "" {
			for _, ext := range extensions {
				candidates = append(candidates, base+ext)
			}
			for _, indexName := range indexNames {
				candidates = append(candidates, filepath.ToSlash(filepath.Join(base, indexName)))
			}
		}
	}
	relativeBase := func() string {
		return normalizeIndexedPath(filepath.Join(filepath.Dir(ref.Path), importPath))
	}

	switch language {
	case "py":
		if strings.HasPrefix(importPath, ".") {
			addBase(pythonRelativeImportBase(ref.Path, importPath), []string{".py"}, []string{"__init__.py"})
		} else {
			addBase(strings.ReplaceAll(importPath, ".", "/"), []string{".py"}, []string{"__init__.py"})
			addBase(filepath.Join(filepath.Dir(ref.Path), strings.ReplaceAll(importPath, ".", "/")), []string{".py"}, []string{"__init__.py"})
		}
	case "js", "jsx", "ts", "tsx", "mjs", "cjs":
		if strings.HasPrefix(importPath, "@/") {
			addBase(strings.TrimPrefix(importPath, "@/"), jsLikeTargetExtensions(), jsLikeIndexFiles())
		}
		if strings.HasPrefix(importPath, ".") {
			addBase(relativeBase(), jsLikeTargetExtensions(), jsLikeIndexFiles())
		}
	case "css", "scss", "sass", "less", "html", "vue", "svelte", "astro":
		if strings.HasPrefix(importPath, ".") || strings.HasPrefix(importPath, "/") {
			addBase(relativeBase(), webAssetTargetExtensions(), nil)
		} else if strings.Contains(importPath, "/") {
			addBase(importPath, webAssetTargetExtensions(), nil)
		}
	case "rb":
		if strings.HasPrefix(importPath, ".") {
			addBase(relativeBase(), []string{".rb"}, nil)
		} else if strings.Contains(importPath, "/") {
			addBase(importPath, []string{".rb"}, nil)
		}
	case "php":
		if strings.HasPrefix(importPath, ".") {
			addBase(relativeBase(), []string{".php"}, nil)
		} else if strings.Contains(importPath, "/") || strings.Contains(importPath, "\\") {
			addBase(strings.ReplaceAll(importPath, "\\", "/"), []string{".php"}, nil)
		}
	case "go":
		if strings.Contains(importPath, "/") {
			for _, suffix := range pathSuffixCandidates(importPath) {
				addBase(suffix, nil, nil)
			}
		}
	case "java", "kt", "kts":
		if strings.Contains(importPath, ".") {
			addBase(strings.ReplaceAll(importPath, ".", "/"), []string{".java", ".kt", ".kts"}, nil)
		}
	case "cs":
		if strings.Contains(importPath, ".") {
			addBase(strings.ReplaceAll(importPath, ".", "/"), []string{".cs"}, nil)
		}
	case "swift":
		if strings.Contains(importPath, ".") || strings.Contains(importPath, "/") {
			addBase(strings.ReplaceAll(importPath, ".", "/"), []string{".swift"}, nil)
		}
	case "rs":
		base := rustImportBase(ref.Path, importPath)
		if base != "" {
			addBase(base, []string{".rs"}, []string{"mod.rs"})
			addBase(filepath.Join("src", base), []string{".rs"}, []string{"mod.rs"})
		}
	default:
		if strings.HasPrefix(importPath, ".") {
			addBase(relativeBase(), nil, nil)
		}
	}
	return uniqueStrings(candidates)
}

func shouldResolveImport(ref ImportReference) bool {
	importPath := strings.TrimSpace(ref.ImportPath)
	if importPath == "" {
		return false
	}
	if strings.HasPrefix(importPath, ".") || strings.HasPrefix(importPath, "/") || strings.HasPrefix(importPath, "@/") {
		return true
	}
	switch NormalizeLanguage(ref.Language) {
	case "py":
		return true
	case "rb", "js", "jsx", "ts", "tsx", "mjs", "cjs", "css", "scss", "sass", "less", "html", "vue", "svelte", "astro", "php":
		return strings.Contains(importPath, "/") || strings.Contains(importPath, "\\")
	case "go":
		return strings.Contains(importPath, "/")
	case "java", "kt", "kts", "cs", "swift":
		return strings.Contains(importPath, ".")
	case "rs":
		return strings.Contains(importPath, "::") || strings.HasPrefix(importPath, "crate") || strings.HasPrefix(importPath, "self") || strings.HasPrefix(importPath, "super")
	default:
		return strings.ContainsAny(importPath, "/\\.")
	}
}

func pythonRelativeImportBase(sourcePath, importPath string) string {
	dots := 0
	for dots < len(importPath) && importPath[dots] == '.' {
		dots++
	}
	remainder := strings.TrimPrefix(importPath[dots:], ".")
	baseDir := filepath.Dir(sourcePath)
	for range max(dots-1, 0) {
		baseDir = filepath.Dir(baseDir)
	}
	if remainder == "" {
		return baseDir
	}
	return filepath.Join(baseDir, strings.ReplaceAll(remainder, ".", "/"))
}

func rustImportBase(sourcePath, importPath string) string {
	importPath = strings.TrimSpace(importPath)
	importPath = strings.TrimPrefix(importPath, "crate::")
	if strings.HasPrefix(importPath, "self::") {
		importPath = strings.TrimPrefix(importPath, "self::")
		return filepath.Join(filepath.Dir(sourcePath), strings.ReplaceAll(importPath, "::", "/"))
	}
	for strings.HasPrefix(importPath, "super::") {
		importPath = strings.TrimPrefix(importPath, "super::")
		sourcePath = filepath.Dir(sourcePath)
	}
	importPath = strings.TrimPrefix(importPath, "::")
	return strings.ReplaceAll(importPath, "::", "/")
}

func pathSuffixCandidates(path string) []string {
	path = normalizeIndexedPath(path)
	parts := strings.Split(path, "/")
	var candidates []string
	for idx := 0; idx < len(parts); idx++ {
		candidate := strings.Join(parts[idx:], "/")
		if candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func jsLikeTargetExtensions() []string {
	return []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json"}
}

func jsLikeIndexFiles() []string {
	return []string{"index.ts", "index.tsx", "index.js", "index.jsx", "index.mjs", "index.cjs"}
}

func webAssetTargetExtensions() []string {
	return []string{".css", ".scss", ".sass", ".less", ".js", ".ts", ".png", ".jpg", ".jpeg", ".svg", ".webp"}
}

func normalizeIndexedPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "./")
	if path == "" || path == "." {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	return path
}
