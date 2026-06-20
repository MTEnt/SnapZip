package core

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type Snippet struct {
	ID       int     `json:"id"`
	Language string  `json:"language"`
	Topic    string  `json:"topic"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
}

var DBPath string

const defaultSearchCandidateLimit = 50

func DBFilePath(dir string) string {
	return filepath.Join(dir, "memory.db")
}

func ResetDB(dir string) error {
	path := DBFilePath(dir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InitDB sets up the main SQLite database and virtual FTS5 index tables
func InitDB(dir string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	DBPath = DBFilePath(dir)
	db, err := sql.Open("sqlite", DBPath)
	if err != nil {
		return nil, err
	}
	initialized := false
	defer func() {
		if !initialized {
			_ = db.Close()
		}
	}()

	// Create real knowledge table
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS knowledge (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		language TEXT,
		topic TEXT,
		content TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		return nil, err
	}

	// Create FTS5 virtual table for knowledge text search
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts USING fts5(
		topic,
		content
	);`)
	if err != nil {
		return nil, err
	}

	// Create negative feedback table
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS negative_feedback (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sentiment TEXT,
		user_input TEXT,
		bot_response TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		return nil, err
	}

	if err := migrateKnowledgeIndex(db); err != nil {
		return nil, err
	}

	initialized = true
	return db, nil
}

// AddKnowledge inserts a new codebase template or config note into SQLite and the FTS5 index
func AddKnowledge(db *sql.DB, language, topic, content string) error {
	language = NormalizeLanguage(language)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO knowledge (language, topic, content)
		VALUES (?, ?, ?)
		ON CONFLICT(language, topic) DO UPDATE SET
			content = excluded.content,
			created_at = CURRENT_TIMESTAMP`,
		language, topic, content,
	)
	if err != nil {
		return err
	}

	var rowID int64
	if err := tx.QueryRow(
		"SELECT id FROM knowledge WHERE language = ? AND topic = ?",
		language, topic,
	).Scan(&rowID); err != nil {
		return err
	}

	if _, err = tx.Exec("DELETE FROM knowledge_fts WHERE rowid = ?", rowID); err != nil {
		return err
	}
	_, err = tx.Exec(
		"INSERT INTO knowledge_fts (rowid, topic, content) VALUES (?, ?, ?)",
		rowID, topic, content,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

type DatabaseStats struct {
	KnowledgeRows int
	FeedbackRows  int
	Languages     []LanguageStat
}

type LanguageStat struct {
	Language string
	Count    int
}

func GetDatabaseStats(db *sql.DB) (DatabaseStats, error) {
	var stats DatabaseStats
	if err := db.QueryRow("SELECT COUNT(*) FROM knowledge").Scan(&stats.KnowledgeRows); err != nil {
		return stats, err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM negative_feedback").Scan(&stats.FeedbackRows); err != nil {
		return stats, err
	}

	rows, err := db.Query(`
		SELECT language, COUNT(*)
		FROM knowledge
		GROUP BY language
		ORDER BY COUNT(*) DESC, language ASC`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var lang LanguageStat
		if err := rows.Scan(&lang.Language, &lang.Count); err != nil {
			return stats, err
		}
		stats.Languages = append(stats.Languages, lang)
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

func migrateKnowledgeIndex(db *sql.DB) error {
	if _, err := db.Exec(`
		DELETE FROM knowledge
		WHERE id NOT IN (
			SELECT MAX(id)
			FROM knowledge
			GROUP BY language, topic
		);`); err != nil {
		return err
	}
	if _, err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS knowledge_language_topic_idx
		ON knowledge(language, topic);`); err != nil {
		return err
	}
	if _, err := db.Exec("DELETE FROM knowledge_fts;"); err != nil {
		return err
	}
	_, err := db.Exec(`
		INSERT INTO knowledge_fts(rowid, topic, content)
		SELECT id, topic, content FROM knowledge;`)
	return err
}

// DetectLanguage parses terms in a query to find the target programming language
func DetectLanguage(prompt string) string {
	for _, token := range languageTokens(prompt) {
		if normalized := NormalizeLanguage(token); defaultCodeLanguages[normalized] {
			return normalized
		}
	}
	return ""
}

// RetrieveSimilarSnippets executes FTS5 full-text lookup and then parallel compression-aware re-ranking.
func RetrieveSimilarSnippets(db *sql.DB, comp Compressor, prompt string, limit int) ([]Snippet, error) {
	detectedLang := DetectLanguage(prompt)

	words := searchTokens(prompt)
	var ftsTokens []string
	for _, w := range words {
		if detectedLang != "" && isLanguageQueryToken(w) {
			continue
		}
		if len(w) > 1 {
			ftsTokens = append(ftsTokens, w)
		}
	}
	if len(ftsTokens) == 0 {
		for _, w := range words {
			if len(w) > 1 {
				ftsTokens = append(ftsTokens, w)
			}
		}
	}
	ftsQuery := strings.Join(ftsTokens, " OR ")

	var candidates []Snippet
	var rows *sql.Rows
	var err error

	if ftsQuery != "" {
		rows, err = db.Query(`
			SELECT k.id, k.language, k.topic, k.content
			FROM knowledge k
			JOIN knowledge_fts f ON k.id = f.rowid
			WHERE knowledge_fts MATCH ?
			ORDER BY f.rank
			LIMIT ?`, ftsQuery, defaultSearchCandidateLimit)
	}

	if err != nil || rows == nil {
		rows, err = db.Query("SELECT id, language, topic, content FROM knowledge ORDER BY id DESC LIMIT ?", defaultSearchCandidateLimit)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	for rows.Next() {
		var s Snippet
		if err := rows.Scan(&s.ID, &s.Language, &s.Topic, &s.Content); err != nil {
			return nil, err
		}
		candidates = append(candidates, s)
	}

	// Parallel QND Re-ranking using Goroutines
	if len(candidates) > 0 {
		numWorkers := min(runtime.NumCPU(), len(candidates))
		if numWorkers < 1 {
			numWorkers = 1
		}
		promptCompressedSize := comp.Compress([]byte(prompt))
		uniqueTokens := uniqueStrings(words)
		jobs := make(chan int, len(candidates))
		var wg sync.WaitGroup

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					qnd := CalculateQNDWithPromptSize(comp, prompt, candidates[idx].Content, promptCompressedSize)
					candidates[idx].Score = relevanceScore(qnd, uniqueTokens, candidates[idx], detectedLang)
				}
			}()
		}

		for i := 0; i < len(candidates); i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()

		// Sort candidates by score (lower QND score = higher similarity)
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Score < candidates[j].Score
		})
	}

	if len(candidates) > limit {
		return candidates[:limit], nil
	}
	return candidates, nil
}

func searchTokens(input string) []string {
	var tokens []string
	for _, token := range strings.FieldsFunc(strings.ToLower(input), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if len(token) > 1 {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func relevanceScore(qnd float64, queryTokens []string, snippet Snippet, detectedLang string) float64 {
	boost := lexicalBoost(queryTokens, snippet)
	if detectedLang != "" && NormalizeLanguage(snippet.Language) == detectedLang {
		boost += 0.08
	}
	return qnd - boost + topicTypePenalty(queryTokens, snippet.Topic)
}

func lexicalBoost(queryTokens []string, snippet Snippet) float64 {
	if len(queryTokens) == 0 {
		return 0
	}

	topic := strings.ToLower(snippet.Topic)
	content := strings.ToLower(snippet.Content)
	topicHits := 0
	contentHits := 0
	for _, token := range queryTokens {
		if strings.Contains(topic, token) {
			topicHits++
		}
		if strings.Contains(content, token) {
			contentHits++
		}
	}

	boost := 0.12 * float64(topicHits)
	boost += 0.035 * float64(contentHits)
	boost += 0.20 * float64(topicHits+contentHits) / float64(len(queryTokens))
	if boost > 0.85 {
		return 0.85
	}
	return boost
}

func topicTypePenalty(queryTokens []string, topic string) float64 {
	lowerTopic := strings.ToLower(topic)
	penalty := 0.0

	if isTestTopic(lowerTopic) && !queryWantsTests(queryTokens) {
		penalty += 0.50
	}
	if isDocTopic(lowerTopic) && !queryWantsDocs(queryTokens) {
		penalty += 0.35
	}
	if isWorkflowTopic(lowerTopic) && !queryWantsWorkflows(queryTokens) {
		penalty += 0.08
	}
	return penalty
}

func isTestTopic(topic string) bool {
	return strings.Contains(topic, "_test.") ||
		strings.Contains(topic, "/test_") ||
		strings.Contains(topic, "/tests/") ||
		strings.Contains(topic, ".test.") ||
		strings.Contains(topic, ".spec.")
}

func isDocTopic(topic string) bool {
	return strings.HasSuffix(topic, ".md") ||
		strings.Contains(topic, "readme") ||
		strings.Contains(topic, "contributing") ||
		strings.Contains(topic, "security") ||
		strings.Contains(topic, "changelog")
}

func isWorkflowTopic(topic string) bool {
	return strings.Contains(topic, ".github/workflows/") ||
		strings.Contains(topic, ".yml") ||
		strings.Contains(topic, ".yaml")
}

func queryWantsTests(tokens []string) bool {
	return tokenSetContains(tokens, "test", "tests", "testing", "spec", "benchmark", "benchmarks")
}

func queryWantsDocs(tokens []string) bool {
	return tokenSetContains(tokens, "readme", "docs", "doc", "documentation", "install", "setup", "contributing", "security", "changelog")
}

func queryWantsWorkflows(tokens []string) bool {
	return tokenSetContains(tokens, "github", "actions", "workflow", "workflows", "ci", "release", "yaml", "yml")
}

func tokenSetContains(tokens []string, values ...string) bool {
	wanted := stringSet(values...)
	for _, token := range tokens {
		if wanted[token] {
			return true
		}
	}
	return false
}

func isLanguageQueryToken(token string) bool {
	cleaned := strings.Trim(token, ".,:;()[]{}<>/\\\"'")
	if cleaned == "" {
		return false
	}
	normalized := NormalizeLanguage(cleaned)
	if defaultCodeLanguages[normalized] {
		return true
	}
	_, ok := languageAliases[normalized]
	return ok
}
