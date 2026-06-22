package core

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type KnowledgeCard struct {
	ID       int     `json:"id,omitempty"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Language string  `json:"language"`
	Path     string  `json:"path"`
	Line     int     `json:"line,omitempty"`
	Summary  string  `json:"summary,omitempty"`
	Weight   float64 `json:"weight,omitempty"`
}

func ReplaceKnowledgeCardsForFile(db *sql.DB, language, path string, content []byte) error {
	language = NormalizeLanguage(language)
	path = strings.TrimSpace(path)
	cards := ExtractKnowledgeCards(language, path, string(content))
	if err := ensureKnowledgeCardStorage(db); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM knowledge_cards WHERE path = ?", path); err != nil {
		return err
	}
	for _, card := range cards {
		if card.Name == "" || card.Kind == "" {
			continue
		}
		_, err := tx.Exec(`
			INSERT INTO knowledge_cards (kind, name, language, path, line, summary, weight)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(kind, name, path, line) DO UPDATE SET
				language = excluded.language,
				summary = excluded.summary,
				weight = excluded.weight,
				created_at = CURRENT_TIMESTAMP`,
			card.Kind,
			card.Name,
			card.Language,
			card.Path,
			card.Line,
			card.Summary,
			card.Weight,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ExtractKnowledgeCards(language, path, content string) []KnowledgeCard {
	language = NormalizeLanguage(language)
	path = normalizeIndexedPath(path)
	var cards []KnowledgeCard
	seen := map[string]bool{}
	add := func(card KnowledgeCard) {
		card.Kind = strings.TrimSpace(strings.ToLower(card.Kind))
		card.Name = strings.TrimSpace(card.Name)
		card.Language = language
		card.Path = path
		card.Summary = compactCardSummary(card.Summary)
		if card.Kind == "" || card.Name == "" || protectedIdentifier(card.Name) {
			return
		}
		if card.Weight <= 0 {
			card.Weight = knowledgeCardKindWeight(card.Kind)
		}
		key := fmt.Sprintf("%s\x00%s\x00%d", card.Kind, strings.ToLower(card.Name), card.Line)
		if seen[key] {
			return
		}
		seen[key] = true
		cards = append(cards, card)
	}

	for _, symbol := range ExtractSymbols(language, path, content) {
		kind := "symbol"
		if isTestPath(path) {
			kind = "test"
		}
		add(KnowledgeCard{
			Kind:    kind,
			Name:    symbol.Name,
			Line:    symbol.Line,
			Summary: strings.TrimSpace(symbol.Signature + "\n" + symbol.Docstring),
		})
	}
	for _, ref := range ExtractSymbolReferences(language, path, content) {
		add(KnowledgeCard{
			Kind:    "usage",
			Name:    ref.Name,
			Line:    ref.Line,
			Summary: ref.Context,
		})
	}
	for _, ref := range ExtractImports(language, path, content) {
		name := ref.ImportPath
		if ref.Alias != "" {
			name = ref.Alias + " " + ref.ImportPath
		}
		add(KnowledgeCard{
			Kind:    "import",
			Name:    name,
			Line:    ref.Line,
			Summary: strings.TrimSpace(ref.Context + " " + ref.TargetPath),
			Weight:  0.88,
		})
	}
	if isConfigPath(path) {
		add(KnowledgeCard{
			Kind:    "config",
			Name:    configCardName(path),
			Line:    1,
			Summary: firstNonEmptyLines(content, 6),
			Weight:  0.82,
		})
	}
	return cards
}

func SearchKnowledgeCards(db *sql.DB, query string, limit int) ([]KnowledgeCard, error) {
	if err := ensureKnowledgeCardSearchIndex(db); err != nil {
		return nil, err
	}
	limit = normalizeResultLimit(limit, 50)
	tokens := codeContextSearchTokens(query)
	if len(tokens) == 0 {
		tokens = searchTokens(query)
	}
	tokens = limitSearchTokens(tokens, 32)
	var matches []KnowledgeCard
	seen := map[int]bool{}

	if ftsQuery := metadataFTSQuery(tokens); ftsQuery != "" {
		rows, err := db.Query(`
			SELECT c.id, c.kind, c.name, c.language, c.path, c.line, c.summary, c.weight
			FROM knowledge_cards c
			JOIN knowledge_cards_fts f ON c.id = f.rowid
			WHERE knowledge_cards_fts MATCH ?
			ORDER BY bm25(knowledge_cards_fts, 1.0, 8.0, 0.5, 2.0, 4.0)
			LIMIT ?`, ftsQuery, metadataFTSCandidateLimit(limit))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			card, err := scanKnowledgeCard(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			seen[card.ID] = true
			if knowledgeCardMatches(card, tokens) {
				matches = append(matches, card)
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
			SELECT id, kind, name, language, path, line, summary, weight
			FROM knowledge_cards`
		args := []any(nil)
		if where, whereArgs := metadataSearchWhere(tokens, "kind", "name", "path", "summary"); where != "" {
			sqlText += " WHERE " + where
			args = append(args, whereArgs...)
		}
		sqlText += " ORDER BY path ASC, line ASC, kind ASC"
		rows, err := db.Query(sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			card, err := scanKnowledgeCard(rows)
			if err != nil {
				return nil, err
			}
			if seen[card.ID] || !knowledgeCardMatches(card, tokens) {
				continue
			}
			matches = append(matches, card)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		left := knowledgeCardScore(matches[i], tokens)
		right := knowledgeCardScore(matches[j], tokens)
		if left == right {
			if matches[i].Path == matches[j].Path {
				return matches[i].Line < matches[j].Line
			}
			return matches[i].Path < matches[j].Path
		}
		return left > right
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func ensureKnowledgeCardStorage(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_cards (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT,
			name TEXT,
			language TEXT,
			path TEXT,
			line INTEGER DEFAULT 0,
			summary TEXT DEFAULT '',
			weight REAL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(kind, name, path, line)
		)`,
		`CREATE INDEX IF NOT EXISTS knowledge_cards_kind_idx ON knowledge_cards(kind)`,
		`CREATE INDEX IF NOT EXISTS knowledge_cards_name_idx ON knowledge_cards(name)`,
		`CREATE INDEX IF NOT EXISTS knowledge_cards_path_idx ON knowledge_cards(path)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureKnowledgeCardSearchIndex(db *sql.DB) error {
	if err := ensureKnowledgeCardStorage(db); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_cards_fts USING fts5(kind, name, language, path, summary)`); err != nil {
		return err
	}
	sourceCount, err := tableRowCount(db, "knowledge_cards")
	if err != nil {
		return err
	}
	ftsCount, err := tableRowCount(db, "knowledge_cards_fts")
	if err != nil {
		return err
	}
	if sourceCount == ftsCount {
		return nil
	}
	if _, err := db.Exec(`DELETE FROM knowledge_cards_fts`); err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO knowledge_cards_fts(rowid, kind, name, language, path, summary)
		SELECT id, kind, name, language, path, summary FROM knowledge_cards`)
	return err
}

func candidateKnowledgeCardRankMap(db *sql.DB, query string, candidates []Snippet, limit int) (map[int]int, map[int]float64, error) {
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	cards, err := SearchKnowledgeCards(db, query, max(limit*8, 80))
	if err != nil {
		return nil, nil, err
	}
	if len(cards) == 0 {
		return nil, nil, nil
	}

	pathRanks := map[string]int{}
	pathScores := map[string]float64{}
	exactLineRanks := map[string]int{}
	exactLineScores := map[string]float64{}
	for idx, card := range cards {
		rank := idx + 1
		path := normalizeIndexedPath(card.Path)
		if path == "" {
			continue
		}
		score := card.Weight / float64(rank)
		if pathRanks[path] == 0 || rank < pathRanks[path] {
			pathRanks[path] = rank
		}
		if score > pathScores[path] {
			pathScores[path] = score
		}
		if card.Line > 0 {
			lineKey := fmt.Sprintf("%s:%d", path, card.Line)
			if exactLineRanks[lineKey] == 0 || rank < exactLineRanks[lineKey] {
				exactLineRanks[lineKey] = rank
			}
			if score*1.15 > exactLineScores[lineKey] {
				exactLineScores[lineKey] = score * 1.15
			}
		}
	}

	ranks := map[int]int{}
	scores := map[int]float64{}
	for _, candidate := range candidates {
		path := normalizeIndexedPath(candidate.Path)
		if path == "" {
			continue
		}
		bestRank := pathRanks[path]
		bestScore := pathScores[path]
		for lineKey, rank := range exactLineRanks {
			if !strings.HasPrefix(lineKey, path+":") {
				continue
			}
			line := knowledgeCardLineFromKey(lineKey)
			if !lineWithinSnippet(line, candidate) {
				continue
			}
			if bestRank == 0 || rank < bestRank {
				bestRank = rank
			}
			if exactLineScores[lineKey] > bestScore {
				bestScore = exactLineScores[lineKey]
			}
		}
		if bestRank > 0 {
			ranks[candidate.ID] = bestRank
			scores[candidate.ID] = bestScore
		}
	}
	return ranks, scores, nil
}

func scanKnowledgeCard(rows interface {
	Scan(dest ...any) error
}) (KnowledgeCard, error) {
	var card KnowledgeCard
	err := rows.Scan(&card.ID, &card.Kind, &card.Name, &card.Language, &card.Path, &card.Line, &card.Summary, &card.Weight)
	return card, err
}

func knowledgeCardMatches(card KnowledgeCard, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	text := strings.ToLower(strings.Join([]string{card.Kind, card.Name, card.Language, card.Path, card.Summary}, " "))
	for _, token := range tokens {
		if token != "" && strings.Contains(text, strings.ToLower(token)) {
			return true
		}
	}
	return false
}

func knowledgeCardScore(card KnowledgeCard, tokens []string) float64 {
	score := card.Weight
	textTokens := stringSet(codeContextSearchTokens(strings.Join([]string{card.Name, card.Path, card.Summary}, " "))...)
	for _, token := range tokens {
		if textTokens[token] {
			score += 0.08
		}
	}
	if card.Kind == "symbol" || card.Kind == "test" {
		score += 0.05
	}
	return score
}

func knowledgeCardKindWeight(kind string) float64 {
	switch kind {
	case "symbol":
		return 1.0
	case "test":
		return 0.95
	case "usage":
		return 0.72
	case "import":
		return 0.88
	case "config":
		return 0.82
	default:
		return 0.5
	}
}

func compactCardSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if len(summary) <= 512 {
		return summary
	}
	return strings.TrimSpace(summary[:512])
}

func isConfigPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	return strings.Contains(lower, "/config/") ||
		strings.Contains(lower, "/configs/") ||
		strings.Contains(base, "config") ||
		strings.Contains(base, "settings") ||
		strings.HasSuffix(base, ".toml") ||
		strings.HasSuffix(base, ".yaml") ||
		strings.HasSuffix(base, ".yml") ||
		strings.HasSuffix(base, ".json") ||
		strings.HasSuffix(base, ".env")
}

func configCardName(path string) string {
	base := filepath.Base(filepath.ToSlash(path))
	if base == "." || base == "/" || base == "" {
		return "config"
	}
	return base
}

func knowledgeCardLineFromKey(key string) int {
	idx := strings.LastIndex(key, ":")
	if idx < 0 || idx+1 >= len(key) {
		return 0
	}
	line := 0
	for _, char := range key[idx+1:] {
		if char < '0' || char > '9' {
			return 0
		}
		line = line*10 + int(char-'0')
	}
	return line
}
