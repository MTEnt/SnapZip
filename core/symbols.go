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

type RepoMapFile struct {
	Path     string   `json:"path"`
	Language string   `json:"language"`
	Symbols  []Symbol `json:"symbols,omitempty"`
}

type RepoMap struct {
	Files []RepoMapFile `json:"files"`
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
	}
	return tx.Commit()
}

func ExtractSymbols(language, path, content string) []Symbol {
	language = NormalizeLanguage(language)
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

func SearchSymbols(db *sql.DB, query string, limit int) ([]Symbol, error) {
	limit = normalizeResultLimit(limit, 20)
	tokens := searchTokens(query)
	rows, err := db.Query(`
		SELECT id, name, kind, signature, language, path, line
		FROM symbols
		ORDER BY path ASC, line ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []Symbol
	for rows.Next() {
		symbol, err := scanSymbol(rows)
		if err != nil {
			return nil, err
		}
		if symbolMatches(symbol, tokens) {
			matches = append(matches, symbol)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return symbolScore(matches[i], tokens) > symbolScore(matches[j], tokens)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
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
	if len(names) == 0 {
		return relatedFilesByPathTokens(db, path, limit)
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
