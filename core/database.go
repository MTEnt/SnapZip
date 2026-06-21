package core

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type Snippet struct {
	ID          int                   `json:"id"`
	Language    string                `json:"language"`
	Topic       string                `json:"topic"`
	Path        string                `json:"path,omitempty"`
	StartLine   int                   `json:"start_line,omitempty"`
	EndLine     int                   `json:"end_line,omitempty"`
	Content     string                `json:"content"`
	ContentHash string                `json:"content_hash,omitempty"`
	SourceMTime int64                 `json:"source_mtime,omitempty"`
	Score       float64               `json:"score"`
	Diagnostics *RetrievalDiagnostics `json:"diagnostics,omitempty"`
}

type KnowledgeEntry struct {
	Language    string
	Topic       string
	Path        string
	StartLine   int
	EndLine     int
	Content     string
	ContentHash string
	SourceMTime int64
}

type RetrieveOptions struct {
	IncludeDiagnostics bool
}

type RetrievalDiagnostics struct {
	FinalRank             int      `json:"final_rank,omitempty"`
	QND                   float64  `json:"qnd"`
	BaseScore             float64  `json:"base_score"`
	FinalScore            float64  `json:"final_score"`
	LexicalBoost          float64  `json:"lexical_boost,omitempty"`
	BM25Boost             float64  `json:"bm25_boost,omitempty"`
	BM25Rank              int      `json:"bm25_rank,omitempty"`
	BM25FBoost            float64  `json:"bm25f_boost,omitempty"`
	BM25FRank             int      `json:"bm25f_rank,omitempty"`
	ExactIdentifierBoost  float64  `json:"exact_identifier_boost,omitempty"`
	StructuredPathBoost   float64  `json:"structured_path_boost,omitempty"`
	StructuredPathRank    int      `json:"structured_path_rank,omitempty"`
	StructuralRerankBoost float64  `json:"structural_rerank_boost,omitempty"`
	StructuralRerankRank  int      `json:"structural_rerank_rank,omitempty"`
	PathTokenBoost        float64  `json:"path_token_boost,omitempty"`
	PathProximityBoost    float64  `json:"path_proximity_boost,omitempty"`
	PathProximityRank     int      `json:"path_proximity_rank,omitempty"`
	GitRecencyBoost       float64  `json:"git_recency_boost,omitempty"`
	LanguageBoost         float64  `json:"language_boost,omitempty"`
	StructureBoost        float64  `json:"structure_boost,omitempty"`
	TopicPenalty          float64  `json:"topic_penalty,omitempty"`
	RankFusionScore       float64  `json:"rank_fusion_score,omitempty"`
	RankFusionBoost       float64  `json:"rank_fusion_boost,omitempty"`
	PrimaryFTSRank        int      `json:"primary_fts_rank,omitempty"`
	QueryPathRank         int      `json:"query_path_rank,omitempty"`
	LexicalCoverageRank   int      `json:"lexical_coverage_rank,omitempty"`
	ProtectedCandidate    bool     `json:"protected_candidate,omitempty"`
	ExternalRerankRank    int      `json:"external_rerank_rank,omitempty"`
	ExternalRRFScore      float64  `json:"external_rrf_score,omitempty"`
	MatchedQueryTokens    []string `json:"matched_query_tokens,omitempty"`
}

var DBPath string
var RerankCmd string

const defaultSearchCandidateLimit = 50
const maxSearchCandidateLimit = 200
const lexicalIDFBlend = 0.12
const lexicalBM25Blend = 0.08
const lexicalBM25FBlend = 0.05
const exactIdentifierBlend = 0.08
const structuredPathBlend = 0.02
const structuralRerankBlend = 0.06
const rankFusionBlend = 0.04
const rankFusionK = 60.0
const rankFusionWindow = 5
const lexicalTailCoverageStart = 4
const lexicalTailCoverageSlots = 1
const relevanceRankFusionWeight = 1.0
const primaryRankFusionWeight = 0.8
const queryPathRankFusionWeight = 0.35
const bm25RankFusionWeight = 0.55
const bm25FRankFusionWeight = 0.45
const structuredRankFusionWeight = 0.45
const structuralRerankFusionWeight = 0.65
const pathProximityRankFusionWeight = 0.50
const lexicalCoverageJaccardWeight = 0.35
const lexicalCoverageBM25Weight = 1.3
const structuralRerankMetadataLimit = 48

type retrievalQueryPlan struct {
	FTSTokens        []string
	FTSPaths         [][]string
	RankingTokens    []string
	StructuredPrompt string
}

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
		path TEXT DEFAULT '',
		start_line INTEGER DEFAULT 0,
		end_line INTEGER DEFAULT 0,
		content_hash TEXT DEFAULT '',
		source_mtime INTEGER DEFAULT 0,
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

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS symbols (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		kind TEXT,
		signature TEXT,
		language TEXT,
		path TEXT,
		line INTEGER,
		docstring TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(path, name, kind, line)
	);`)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS symbol_refs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		language TEXT,
		path TEXT,
		line INTEGER,
		context TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(path, name, line)
	);`)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS import_refs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		import_path TEXT,
		alias TEXT,
		language TEXT,
		path TEXT,
		target_path TEXT DEFAULT '',
		line INTEGER,
		context TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(path, import_path, line)
	);`)
	if err != nil {
		return nil, err
	}

	hasDoc, err := hasColumn(db, "symbols_fts", "docstring")
	if err == nil && !hasDoc {
		_, _ = db.Exec("DROP TABLE IF EXISTS symbols_fts")
	}

	if err := ensureMetadataFTSTables(db); err != nil {
		return nil, err
	}
	if err := migrateKnowledgeIndex(db); err != nil {
		return nil, err
	}
	if err := migrateImportRefs(db); err != nil {
		return nil, err
	}
	if err := migrateSymbols(db); err != nil {
		return nil, err
	}
	if err := ensureSearchIndexes(db); err != nil {
		return nil, err
	}
	if err := syncMetadataFTS(db); err != nil {
		return nil, err
	}

	initialized = true
	return db, nil
}

// AddKnowledge inserts a new codebase template or config note into SQLite and the FTS5 index
func AddKnowledge(db *sql.DB, language, topic, content string) error {
	_, err := AddKnowledgeEntry(db, KnowledgeEntry{
		Language: language,
		Topic:    topic,
		Path:     pathFromTopic(topic),
		Content:  content,
	})
	return err
}

func AddKnowledgeEntry(db *sql.DB, entry KnowledgeEntry) (bool, error) {
	language := NormalizeLanguage(entry.Language)
	entry.Language = language
	entry.Topic = strings.TrimSpace(entry.Topic)
	entry.Path = strings.TrimSpace(entry.Path)
	if entry.Topic == "" && entry.Path != "" {
		entry.Topic = "Source file: " + entry.Path
	}
	if entry.Path == "" {
		entry.Path = pathFromTopic(entry.Topic)
	}
	if entry.ContentHash == "" {
		entry.ContentHash = contentHash([]byte(entry.Content))
	}
	if entry.StartLine < 0 {
		entry.StartLine = 0
	}
	if entry.EndLine < entry.StartLine {
		entry.EndLine = entry.StartLine
	}

	changed := true
	var existingHash string
	if err := db.QueryRow(
		"SELECT content_hash FROM knowledge WHERE language = ? AND topic = ?",
		entry.Language,
		entry.Topic,
	).Scan(&existingHash); err == nil && existingHash == entry.ContentHash {
		changed = false
	}

	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO knowledge (language, topic, content, path, start_line, end_line, content_hash, source_mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(language, topic) DO UPDATE SET
			content = excluded.content,
			path = excluded.path,
			start_line = excluded.start_line,
			end_line = excluded.end_line,
			content_hash = excluded.content_hash,
			source_mtime = excluded.source_mtime,
			created_at = CURRENT_TIMESTAMP`,
		entry.Language,
		entry.Topic,
		entry.Content,
		entry.Path,
		entry.StartLine,
		entry.EndLine,
		entry.ContentHash,
		entry.SourceMTime,
	)
	if err != nil {
		return false, err
	}

	var rowID int64
	if err := tx.QueryRow(
		"SELECT id FROM knowledge WHERE language = ? AND topic = ?",
		entry.Language,
		entry.Topic,
	).Scan(&rowID); err != nil {
		return false, err
	}

	if _, err = tx.Exec("DELETE FROM knowledge_fts WHERE rowid = ?", rowID); err != nil {
		return false, err
	}
	_, err = tx.Exec(
		"INSERT INTO knowledge_fts (rowid, topic, content) VALUES (?, ?, ?)",
		rowID, entry.Topic, entry.Content,
	)
	if err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

type DatabaseStats struct {
	KnowledgeRows       int            `json:"knowledge_rows"`
	FeedbackRows        int            `json:"feedback_rows"`
	SymbolRows          int            `json:"symbol_rows"`
	SymbolReferenceRows int            `json:"symbol_reference_rows"`
	ImportRows          int            `json:"import_rows"`
	Languages           []LanguageStat `json:"languages"`
}

type LanguageStat struct {
	Language string `json:"language"`
	Count    int    `json:"count"`
}

func GetDatabaseStats(db *sql.DB) (DatabaseStats, error) {
	var stats DatabaseStats
	if err := db.QueryRow("SELECT COUNT(*) FROM knowledge").Scan(&stats.KnowledgeRows); err != nil {
		return stats, err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM negative_feedback").Scan(&stats.FeedbackRows); err != nil {
		return stats, err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols").Scan(&stats.SymbolRows); err != nil {
		return stats, err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM symbol_refs").Scan(&stats.SymbolReferenceRows); err != nil {
		return stats, err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM import_refs").Scan(&stats.ImportRows); err != nil {
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
	for _, column := range []struct {
		name string
		def  string
	}{
		{"path", "TEXT DEFAULT ''"},
		{"start_line", "INTEGER DEFAULT 0"},
		{"end_line", "INTEGER DEFAULT 0"},
		{"content_hash", "TEXT DEFAULT ''"},
		{"source_mtime", "INTEGER DEFAULT 0"},
	} {
		if err := ensureKnowledgeColumn(db, column.name, column.def); err != nil {
			return err
		}
	}

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

func ensureKnowledgeColumn(db *sql.DB, name, definition string) error {
	return ensureTableColumn(db, "knowledge", name, definition)
}

func migrateImportRefs(db *sql.DB) error {
	return ensureTableColumn(db, "import_refs", "target_path", "TEXT DEFAULT ''")
}

func ensureSearchIndexes(db *sql.DB) error {
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS knowledge_path_idx ON knowledge(path)",
		"CREATE INDEX IF NOT EXISTS symbols_name_idx ON symbols(name)",
		"CREATE INDEX IF NOT EXISTS symbols_path_idx ON symbols(path)",
		"CREATE INDEX IF NOT EXISTS symbol_refs_name_idx ON symbol_refs(name)",
		"CREATE INDEX IF NOT EXISTS symbol_refs_path_idx ON symbol_refs(path)",
		"CREATE INDEX IF NOT EXISTS import_refs_import_path_idx ON import_refs(import_path)",
		"CREATE INDEX IF NOT EXISTS import_refs_alias_idx ON import_refs(alias)",
		"CREATE INDEX IF NOT EXISTS import_refs_path_idx ON import_refs(path)",
		"CREATE INDEX IF NOT EXISTS import_refs_target_path_idx ON import_refs(target_path)",
	}
	for _, stmt := range indexes {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureMetadataFTSTables(db *sql.DB) error {
	statements := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(name, kind, signature, language, path, docstring)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS symbol_refs_fts USING fts5(name, language, path, context)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS import_refs_fts USING fts5(import_path, alias, language, path, target_path, context)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func syncMetadataFTS(db *sql.DB) error {
	tables := []struct {
		source string
		fts    string
	}{
		{"symbols", "symbols_fts"},
		{"symbol_refs", "symbol_refs_fts"},
		{"import_refs", "import_refs_fts"},
	}
	for _, table := range tables {
		sourceCount, err := tableRowCount(db, table.source)
		if err != nil {
			return err
		}
		ftsCount, err := tableRowCount(db, table.fts)
		if err != nil {
			return err
		}
		if sourceCount != ftsCount {
			return rebuildMetadataFTS(db)
		}
	}
	return nil
}

func rebuildMetadataFTS(db *sql.DB) error {
	statements := []string{
		`DELETE FROM symbols_fts`,
		`INSERT INTO symbols_fts(rowid, name, kind, signature, language, path, docstring)
		 SELECT id, name, kind, signature, language, path, docstring FROM symbols`,
		`DELETE FROM symbol_refs_fts`,
		`INSERT INTO symbol_refs_fts(rowid, name, language, path, context)
		 SELECT id, name, language, path, context FROM symbol_refs`,
		`DELETE FROM import_refs_fts`,
		`INSERT INTO import_refs_fts(rowid, import_path, alias, language, path, target_path, context)
		 SELECT id, import_path, alias, language, path, target_path, context FROM import_refs`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func migrateSymbols(db *sql.DB) error {
	return ensureTableColumn(db, "symbols", "docstring", "TEXT DEFAULT ''")
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if strings.ToLower(columnName) == strings.ToLower(column) {
			return true, nil
		}
	}
	return false, nil
}

func tableRowCount(db *sql.DB, table string) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
	return count, err
}

func ensureTableColumn(db *sql.DB, table, name, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + definition)
	return err
}

func metadataSearchWhere(tokens []string, columns ...string) (string, []any) {
	tokens = metadataSearchTokens(tokens)
	if len(tokens) == 0 || len(columns) == 0 {
		return "", nil
	}

	var clauses []string
	var args []any
	for _, token := range tokens {
		pattern := "%" + escapeSQLLike(token) + "%"
		var tokenClauses []string
		for _, column := range columns {
			tokenClauses = append(tokenClauses, column+" LIKE ? ESCAPE '\\'")
			args = append(args, pattern)
		}
		clauses = append(clauses, "("+strings.Join(tokenClauses, " OR ")+")")
	}
	return strings.Join(clauses, " OR "), args
}

func metadataSearchTokens(tokens []string) []string {
	tokens = uniqueStrings(tokens)
	if len(tokens) > 16 {
		return tokens[:16]
	}
	return tokens
}

func metadataFTSQuery(tokens []string) string {
	tokens = metadataSearchTokens(tokens)
	var terms []string
	for _, token := range tokens {
		if len(token) > 1 {
			terms = append(terms, token+"*")
		}
	}
	return strings.Join(terms, " OR ")
}

func metadataFTSCandidateLimit(limit int) int {
	return min(max(limit*10, 50), 500)
}

func escapeSQLLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
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

func calculatePathProximityBoost(currentPath, candidatePath string) float64 {
	currentPath = strings.ReplaceAll(currentPath, "\\", "/")
	candidatePath = strings.ReplaceAll(candidatePath, "\\", "/")

	currentParts := strings.Split(currentPath, "/")
	candidateParts := strings.Split(candidatePath, "/")

	if len(currentParts) > 0 {
		currentParts = currentParts[:len(currentParts)-1]
	}
	if len(candidateParts) > 0 {
		candidateParts = candidateParts[:len(candidateParts)-1]
	}

	matching := 0
	minLen := len(currentParts)
	if len(candidateParts) < minLen {
		minLen = len(candidateParts)
	}

	for i := 0; i < minLen; i++ {
		if strings.ToLower(currentParts[i]) == strings.ToLower(candidateParts[i]) {
			matching++
		} else {
			break
		}
	}

	if matching == 0 {
		return 0.0
	}

	boost := float64(matching) * 0.04
	if boost > 0.20 {
		boost = 0.20
	}
	return boost
}

// RetrieveSimilarSnippets executes FTS5 full-text lookup and then parallel compression-aware re-ranking.
func RetrieveSimilarSnippets(db *sql.DB, comp Compressor, prompt string, limit int) ([]Snippet, error) {
	return RetrieveSimilarSnippetsWithOptions(db, comp, prompt, limit, RetrieveOptions{})
}

// RetrieveSimilarSnippetsWithOptions executes FTS5 full-text lookup and then parallel compression-aware re-ranking.
func RetrieveSimilarSnippetsWithOptions(db *sql.DB, comp Compressor, prompt string, limit int, options RetrieveOptions) ([]Snippet, error) {
	if RerankCmd != "" {
		originalRerankCmd := RerankCmd
		RerankCmd = ""
		defer func() { RerankCmd = originalRerankCmd }()

		fetchLimit := limit * 3
		if fetchLimit < 15 {
			fetchLimit = 15
		}
		snippets, err := RetrieveSimilarSnippetsWithOptions(db, comp, prompt, fetchLimit, options)
		if err != nil {
			return nil, err
		}

		cleanPrompt := prompt
		if strings.HasPrefix(cleanPrompt, "--current-path:") {
			parts := strings.SplitN(cleanPrompt, "\n", 2)
			if len(parts) == 2 {
				cleanPrompt = parts[1]
			}
		}
		reranked, err := runExternalReranker(originalRerankCmd, cleanPrompt, snippets)
		if err != nil {
			return nil, err
		}

		baseRankMap := make(map[int]int)
		for idx, s := range snippets {
			baseRankMap[s.ID] = idx + 1
		}
		rerankRankMap := make(map[int]int)
		for idx, s := range reranked {
			rerankRankMap[s.ID] = idx + 1
		}

		type rrfSnippet struct {
			snippet Snippet
			score   float64
		}
		var rrfList []rrfSnippet
		for _, s := range snippets {
			baseRank := baseRankMap[s.ID]
			rerankRank := rerankRankMap[s.ID]
			if rerankRank == 0 {
				rerankRank = len(snippets) + 1
			}
			score := 1.0/(20.0+float64(baseRank)) + 1.0/(20.0+float64(rerankRank))
			if options.IncludeDiagnostics && s.Diagnostics != nil {
				s.Diagnostics.ExternalRerankRank = rerankRank
				s.Diagnostics.ExternalRRFScore = score
			}
			rrfList = append(rrfList, rrfSnippet{snippet: s, score: score})
		}

		sort.Slice(rrfList, func(i, j int) bool {
			return rrfList[i].score > rrfList[j].score
		})

		var finalSnippets []Snippet
		for _, item := range rrfList {
			finalSnippets = append(finalSnippets, item.snippet)
		}

		if len(finalSnippets) > limit {
			finalSnippets = finalSnippets[:limit]
		}
		annotateFinalRanks(finalSnippets)
		return finalSnippets, nil
	}
	var currentPath string
	if strings.HasPrefix(prompt, "--current-path:") {
		parts := strings.SplitN(prompt, "\n", 2)
		if len(parts) == 2 {
			currentPath = strings.TrimSpace(strings.TrimPrefix(parts[0], "--current-path:"))
			prompt = parts[1]
		}
	}
	detectedLang := DetectLanguage(prompt)
	limit = normalizeResultLimit(limit, 3)
	candidateLimit := searchCandidateLimit(limit)

	queryPlan := planRetrievalQuery(prompt)
	words := searchTokens(prompt)
	primaryWords := primarySearchTokens(prompt)
	structuredPromptPlan := ""
	if !looksLikeCodeContext(prompt) {
		if len(queryPlan.RankingTokens) > 0 {
			words = queryPlan.RankingTokens
		}
		if len(queryPlan.FTSTokens) > 0 {
			primaryWords = queryPlan.FTSTokens
		}
		structuredPromptPlan = queryPlan.StructuredPrompt
	}
	ftsQuery := snippetFTSQuery(primaryWords, detectedLang)

	var candidates []Snippet
	var rows *sql.Rows
	var err error
	usedFTS := false
	usedExpandedFTSFallback := false

	if ftsQuery != "" {
		usedFTS = true
		rows, err = queryKnowledgeFTS(db, ftsQuery, candidateLimit)
	}

	if err != nil || rows == nil {
		usedFTS = false
		rows, err = db.Query(`
			SELECT id, language, topic, content, path, start_line, end_line, content_hash, source_mtime
			FROM knowledge
			ORDER BY id DESC
			LIMIT ?`, candidateLimit)
		if err != nil {
			return nil, err
		}
	}

	candidates, err = appendSnippetRows(candidates, rows)
	if err != nil {
		return nil, err
	}
	if usedFTS && len(candidates) == 0 {
		expandedFTSQuery := snippetFTSQuery(words, detectedLang)
		if expandedFTSQuery != "" && expandedFTSQuery != ftsQuery {
			rows, err = queryKnowledgeFTS(db, expandedFTSQuery, candidateLimit)
			if err != nil {
				return nil, err
			}
			candidates, err = appendSnippetRows(nil, rows)
			if err != nil {
				return nil, err
			}
			usedExpandedFTSFallback = len(candidates) > 0
		}
	}

	queryPathCandidateRanks := map[int]int{}
	if usedFTS {
		skipQueries := map[string]bool{}
		if ftsQuery != "" {
			skipQueries[ftsQuery] = true
		}
		if usedExpandedFTSFallback {
			if expandedFTSQuery := snippetFTSQuery(words, detectedLang); expandedFTSQuery != "" {
				skipQueries[expandedFTSQuery] = true
			}
			queryPathCandidateRanks = candidateIDRankMap(candidates)
		}
		candidates, queryPathCandidateRanks, err = appendFTSPathCandidates(db, queryPlan.FTSPaths, detectedLang, candidates, queryPathCandidateRanks, skipQueries, candidateLimit)
		if err != nil {
			return nil, err
		}
	}

	primaryCandidateIDs := candidateIDSet(candidates)
	primaryCandidateRanks := map[int]int{}
	if usedFTS {
		primaryCandidateRanks = candidateIDRankMap(candidates)
	}
	structuredPathRanks := map[string]int{}
	if len(candidates) < candidateLimit {
		structuredPrompt := structuredSearchPrompt(prompt, structuredPromptPlan, primaryWords, usedExpandedFTSFallback)
		structuredPaths, err := structuredCandidatePaths(db, structuredPrompt, min(candidateLimit, 60))
		if err != nil {
			return nil, err
		}
		structuredPathRanks = rankedPathMap(structuredPaths)
		candidates, err = appendStructuredSearchCandidatesForPaths(db, structuredPaths, candidates, candidateLimit)
		if err != nil {
			return nil, err
		}
	}
	if usedFTS && looksLikeCodeContext(prompt) && len(candidates) < candidateLimit {
		candidates, err = appendSmallCorpusSearchCandidates(db, candidates, candidateLimit)
		if err != nil {
			return nil, err
		}
	}
	if len(candidates) > 0 && len(candidates) < limit {
		candidates, err = backfillSearchCandidates(db, candidates, candidateLimit)
		if err != nil {
			return nil, err
		}
	}
	if usedFTS && len(candidates) == 0 && looksLikeCodeContext(prompt) {
		candidates, err = backfillSearchCandidates(db, candidates, candidateLimit)
		if err != nil {
			return nil, err
		}
	}
	if usedFTS && len(candidates) == 0 {
		return nil, nil
	}

	protectedCandidateID := 0
	lexicalCoverageRanks := map[int]int{}

	// Parallel QND Re-ranking using Goroutines
	if len(candidates) > 0 {
		numWorkers := min(runtime.NumCPU(), len(candidates))
		if numWorkers < 1 {
			numWorkers = 1
		}
		promptCompressedSize := comp.Compress([]byte(prompt))
		rankingTokens := primaryWords
		if usedExpandedFTSFallback || len(rankingTokens) == 0 {
			rankingTokens = words
		}
		uniqueTokens := uniqueStrings(rankingTokens)
		structureIntent := queryStructureIntent(prompt)
		structureWeight := queryStructureBoostWeight(prompt, uniqueTokens)
		tokenWeights := queryTokenWeights(uniqueTokens, candidates)
		bm25Boosts := candidateBM25Boosts(uniqueTokens, candidates)
		bm25CandidateRanks := candidateValueRankMap(candidates, bm25Boosts, true)
		bm25FBoosts := candidateBM25FBoosts(uniqueTokens, candidates)
		bm25FCandidateRanks := candidateValueRankMap(candidates, bm25FBoosts, true)
		jaccardCandidateRanks := candidateJaccardRankMap(uniqueTokens, candidates)
		lexicalCoverageRanks = combinedLexicalCoverageRankMap(jaccardCandidateRanks, bm25CandidateRanks, bm25FCandidateRanks)
		exactIdentifierBoosts := candidateExactIdentifierBoosts(prompt, candidates)
		structuredBoosts := candidateStructuredPathBoosts(candidates, structuredPathRanks, primaryCandidateIDs)
		structuralRerankBoosts, structuralRerankRanks, err := candidateStructuralRerankBoosts(db, prompt, candidates)
		if err != nil {
			return nil, err
		}
		baseScores := make([]float64, len(candidates))
		var recentWeights map[string]float64
		if currentPath != "" {
			if root, err := getDBDir(db); err == nil && root != "" {
				recentWeights, _ = gitRecentFiles(root)
			}
		}
		jobs := make(chan int, len(candidates))
		var wg sync.WaitGroup

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					qnd := CalculateQNDWithPromptSize(comp, prompt, candidates[idx].Content, promptCompressedSize)
					pathProximityBoost := 0.0
					if currentPath != "" {
						pathProximityBoost = calculatePathProximityBoost(currentPath, candidates[idx].Path)
					}
					structuralBoost := structuredBoosts[idx] + structuralRerankBoosts[idx] + exactIdentifierBoosts[idx] + pathProximityBoost
					gitBoost := 0.0
					if recentWeights != nil {
						candidatePath := filepath.ToSlash(candidates[idx].Path)
						for gitPath, weight := range recentWeights {
							if strings.HasSuffix(gitPath, candidatePath) || strings.HasSuffix(candidatePath, gitPath) {
								gitBoost = weight
								break
							}
						}
					}
					components := candidateScoreComponents(uniqueTokens, tokenWeights, 0, structuralBoost, candidates[idx], detectedLang, structureIntent, structureWeight)
					baseScore := components.score(qnd) - gitBoost
					baseScores[idx] = baseScore
					candidates[idx].Score = baseScore - bm25Boosts[idx] - bm25FBoosts[idx]
					if options.IncludeDiagnostics {
						candidates[idx].Diagnostics = &RetrievalDiagnostics{
							QND:                   qnd,
							BaseScore:             baseScore,
							FinalScore:            candidates[idx].Score,
							LexicalBoost:          components.LexicalBoost,
							BM25Boost:             bm25Boosts[idx],
							BM25Rank:              bm25CandidateRanks[candidates[idx].ID],
							BM25FBoost:            bm25FBoosts[idx],
							BM25FRank:             bm25FCandidateRanks[candidates[idx].ID],
							ExactIdentifierBoost:  exactIdentifierBoosts[idx],
							StructuredPathBoost:   structuredBoosts[idx],
							StructuredPathRank:    structuredPathRanks[normalizeIndexedPath(candidates[idx].Path)],
							StructuralRerankBoost: structuralRerankBoosts[idx],
							StructuralRerankRank:  structuralRerankRanks[candidates[idx].ID],
							PathTokenBoost:        components.PathTokenBoost,
							PathProximityBoost:    pathProximityBoost,
							GitRecencyBoost:       gitBoost,
							LanguageBoost:         components.LanguageBoost,
							StructureBoost:        components.StructureBoost,
							TopicPenalty:          components.TopicPenalty,
							PrimaryFTSRank:        primaryCandidateRanks[candidates[idx].ID],
							QueryPathRank:         queryPathCandidateRanks[candidates[idx].ID],
							MatchedQueryTokens:    matchedDiagnosticQueryTokens(uniqueTokens, candidates[idx]),
						}
					}
				}
			}()
		}

		for i := 0; i < len(candidates); i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()

		if usedFTS && len(primaryCandidateIDs) > 0 {
			protectedCandidateID = topCandidateIDByBaseScore(candidates, baseScores, primaryCandidateIDs)
			if options.IncludeDiagnostics {
				for idx := range candidates {
					if candidates[idx].ID == protectedCandidateID && candidates[idx].Diagnostics != nil {
						candidates[idx].Diagnostics.ProtectedCandidate = true
						break
					}
				}
			}
		}

		// Sort candidates by score (lower QND score = higher similarity)
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Score < candidates[j].Score
		})
		pathProximityRanks := map[int]int{}
		if currentPath != "" {
			type pathCandidate struct {
				id    int
				boost float64
			}
			var pathCands []pathCandidate
			for idx := range candidates {
				boost := calculatePathProximityBoost(currentPath, candidates[idx].Path)
				if boost > 0 {
					pathCands = append(pathCands, pathCandidate{id: candidates[idx].ID, boost: boost})
				}
			}
			sort.Slice(pathCands, func(i, j int) bool {
				return pathCands[i].boost > pathCands[j].boost
			})
			for r, pc := range pathCands {
				pathProximityRanks[pc.id] = r + 1
			}
			if options.IncludeDiagnostics {
				for idx := range candidates {
					if candidates[idx].Diagnostics != nil {
						candidates[idx].Diagnostics.PathProximityRank = pathProximityRanks[candidates[idx].ID]
					}
				}
			}
		}
		fusionDiagnostics := applyCandidateRankFusion(candidates, primaryCandidateRanks, queryPathCandidateRanks, bm25CandidateRanks, bm25FCandidateRanks, structuredPathRanks, structuralRerankRanks, pathProximityRanks)
		if options.IncludeDiagnostics {
			for idx := range candidates {
				diagnostics := candidates[idx].Diagnostics
				if diagnostics == nil {
					continue
				}
				if fusion := fusionDiagnostics[candidates[idx].ID]; fusion.Score > 0 {
					diagnostics.RankFusionScore = fusion.Score
					diagnostics.RankFusionBoost = fusion.Boost
				}
				diagnostics.FinalScore = candidates[idx].Score
			}
		}
	}

	if usedFTS && len(primaryCandidateIDs) > 0 {
		ranked := rankSearchCandidates(candidates, primaryCandidateIDs, protectedCandidateID, lexicalCoverageRanks, limit)
		if options.IncludeDiagnostics {
			for idx := range ranked {
				if ranked[idx].Diagnostics != nil {
					ranked[idx].Diagnostics.LexicalCoverageRank = lexicalCoverageRanks[ranked[idx].ID]
				}
			}
		}
		annotateFinalRanks(ranked)
		return ranked, nil
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	annotateFinalRanks(candidates)
	return candidates, nil
}

func annotateFinalRanks(snippets []Snippet) {
	for idx := range snippets {
		if snippets[idx].Diagnostics == nil {
			continue
		}
		snippets[idx].Diagnostics.FinalRank = idx + 1
		snippets[idx].Diagnostics.FinalScore = snippets[idx].Score
	}
}

func matchedDiagnosticQueryTokens(queryTokens []string, candidate Snippet) []string {
	if len(queryTokens) == 0 {
		return nil
	}
	candidateTokens := stringSet(documentSearchTokens(candidate.Path)...)
	for _, token := range documentSearchTokens(candidate.Topic) {
		candidateTokens[token] = true
	}
	for _, token := range documentSearchTokens(candidate.Content) {
		candidateTokens[token] = true
	}
	var matched []string
	seen := map[string]bool{}
	for _, token := range queryTokens {
		if token == "" || seen[token] || !candidateTokens[token] {
			continue
		}
		matched = append(matched, token)
		seen[token] = true
	}
	return matched
}

func queryKnowledgeFTS(db *sql.DB, ftsQuery string, candidateLimit int) (*sql.Rows, error) {
	return db.Query(`
		SELECT k.id, k.language, k.topic, k.content
			, k.path, k.start_line, k.end_line, k.content_hash, k.source_mtime
		FROM knowledge k
		JOIN knowledge_fts f ON k.id = f.rowid
		WHERE knowledge_fts MATCH ?
		ORDER BY f.rank
		LIMIT ?`, ftsQuery, candidateLimit)
}

type rankFusionDiagnostic struct {
	Score float64
	Boost float64
}

func applyCandidateRankFusion(candidates []Snippet, primaryRanks map[int]int, queryPathRanks map[int]int, bm25Ranks map[int]int, bm25FRanks map[int]int, structuredPathRanks map[string]int, structuralRanks map[int]int, pathProximityRanks map[int]int) map[int]rankFusionDiagnostic {
	if len(candidates) < 2 {
		return nil
	}

	window := min(rankFusionWindow, len(candidates))
	fusionWindow := candidates[:window]
	fusionScores := candidateRankFusionScores(fusionWindow, primaryRanks, queryPathRanks, bm25Ranks, bm25FRanks, structuredPathRanks, structuralRanks, pathProximityRanks)
	maxScore := 0.0
	for _, score := range fusionScores {
		if score > maxScore {
			maxScore = score
		}
	}
	if maxScore == 0 {
		return nil
	}

	diagnostics := make(map[int]rankFusionDiagnostic, len(fusionScores))
	for idx := range fusionWindow {
		boost := rankFusionBlend * fusionScores[fusionWindow[idx].ID] / maxScore
		fusionWindow[idx].Score -= boost
		diagnostics[fusionWindow[idx].ID] = rankFusionDiagnostic{
			Score: fusionScores[fusionWindow[idx].ID],
			Boost: boost,
		}
	}
	sort.SliceStable(fusionWindow, func(i, j int) bool {
		if fusionWindow[i].Score == fusionWindow[j].Score {
			return fusionWindow[i].ID < fusionWindow[j].ID
		}
		return fusionWindow[i].Score < fusionWindow[j].Score
	})
	return diagnostics
}

func candidateRankFusionScores(candidates []Snippet, primaryRanks map[int]int, queryPathRanks map[int]int, bm25Ranks map[int]int, bm25FRanks map[int]int, structuredPathRanks map[string]int, structuralRanks map[int]int, pathProximityRanks map[int]int) map[int]float64 {
	scores := make(map[int]float64, len(candidates))
	for idx, candidate := range candidates {
		if candidate.ID == 0 {
			continue
		}
		addRankFusionScore(scores, candidate.ID, idx+1, relevanceRankFusionWeight)
		if rank := primaryRanks[candidate.ID]; rank > 0 {
			addRankFusionScore(scores, candidate.ID, rank, primaryRankFusionWeight)
		}
		if rank := queryPathRanks[candidate.ID]; rank > 0 {
			addRankFusionScore(scores, candidate.ID, rank, queryPathRankFusionWeight)
		}
		if rank := bm25Ranks[candidate.ID]; rank > 0 {
			addRankFusionScore(scores, candidate.ID, rank, bm25RankFusionWeight)
		}
		if rank := bm25FRanks[candidate.ID]; rank > 0 {
			addRankFusionScore(scores, candidate.ID, rank, bm25FRankFusionWeight)
		}
		if rank := structuredPathRanks[normalizeIndexedPath(candidate.Path)]; rank > 0 {
			addRankFusionScore(scores, candidate.ID, rank, structuredRankFusionWeight)
		}
		if rank := structuralRanks[candidate.ID]; rank > 0 {
			addRankFusionScore(scores, candidate.ID, rank, structuralRerankFusionWeight)
		}
		if rank := pathProximityRanks[candidate.ID]; rank > 0 {
			addRankFusionScore(scores, candidate.ID, rank, pathProximityRankFusionWeight)
		}
	}
	return scores
}

func addRankFusionScore(scores map[int]float64, id int, rank int, weight float64) {
	if id == 0 || rank <= 0 || weight <= 0 {
		return
	}
	scores[id] += weight / (rankFusionK + float64(rank))
}

func rankSearchCandidates(candidates []Snippet, primaryCandidateIDs map[int]bool, protectedID int, lexicalRanks map[int]int, limit int) []Snippet {
	if len(candidates) <= limit {
		return candidates
	}

	protectedLimit := min(1, limit)
	ranked := make([]Snippet, 0, len(candidates))
	used := make(map[int]bool, limit)

	if protectedID != 0 {
		for _, candidate := range candidates {
			if candidate.ID != protectedID || !primaryCandidateIDs[candidate.ID] {
				continue
			}
			ranked = append(ranked, candidate)
			used[candidate.ID] = true
			break
		}
	}

	for _, candidate := range candidates {
		if !primaryCandidateIDs[candidate.ID] {
			continue
		}
		if used[candidate.ID] {
			continue
		}
		ranked = append(ranked, candidate)
		used[candidate.ID] = true
		if len(ranked) >= protectedLimit || len(ranked) >= limit {
			break
		}
	}
	for _, candidate := range candidates {
		if used[candidate.ID] {
			continue
		}
		ranked = append(ranked, candidate)
	}
	ranked = applyTailRankCoverage(ranked, lexicalRanks, limit)
	return diversifyRankedSearchCandidates(ranked, limit)
}

func applyTailRankCoverage(candidates []Snippet, lexicalRanks map[int]int, limit int) []Snippet {
	if limit <= lexicalTailCoverageStart || len(candidates) <= lexicalTailCoverageStart || len(lexicalRanks) == 0 {
		return candidates
	}

	prefix := min(lexicalTailCoverageStart, len(candidates), limit)
	slots := min(lexicalTailCoverageSlots, limit-prefix)
	if slots <= 0 {
		return candidates
	}

	used := make(map[int]bool, limit)
	covered := make([]Snippet, 0, len(candidates))
	for _, candidate := range candidates[:prefix] {
		covered = append(covered, candidate)
		used[candidate.ID] = true
	}

	tail := append([]Snippet(nil), candidates[prefix:]...)
	sort.SliceStable(tail, func(i, j int) bool {
		leftRank := lexicalRanks[tail[i].ID]
		rightRank := lexicalRanks[tail[j].ID]
		if leftRank == 0 && rightRank == 0 {
			return tail[i].Score < tail[j].Score
		}
		if leftRank == 0 {
			return false
		}
		if rightRank == 0 {
			return true
		}
		if leftRank == rightRank {
			return tail[i].Score < tail[j].Score
		}
		return leftRank < rightRank
	})

	tailCovered := 0
	for _, candidate := range tail {
		if used[candidate.ID] || lexicalRanks[candidate.ID] == 0 {
			continue
		}
		covered = append(covered, candidate)
		used[candidate.ID] = true
		tailCovered++
		if tailCovered >= slots {
			break
		}
	}
	for _, candidate := range candidates[prefix:] {
		if used[candidate.ID] {
			continue
		}
		covered = append(covered, candidate)
		used[candidate.ID] = true
	}
	return covered
}

func combinedLexicalCoverageRankMap(jaccardRanks, bm25Ranks map[int]int, extraRankMaps ...map[int]int) map[int]int {
	scores := map[int]float64{}
	bestRanks := map[int]int{}
	add := func(ranks map[int]int, weight float64) {
		for id, rank := range ranks {
			if id == 0 || rank <= 0 {
				continue
			}
			addRankFusionScore(scores, id, rank, weight)
			if bestRanks[id] == 0 || rank < bestRanks[id] {
				bestRanks[id] = rank
			}
		}
	}
	add(jaccardRanks, lexicalCoverageJaccardWeight)
	add(bm25Ranks, lexicalCoverageBM25Weight)
	for _, ranks := range extraRankMaps {
		add(ranks, lexicalCoverageBM25Weight)
	}
	if len(scores) == 0 {
		return nil
	}

	type rankedCandidate struct {
		id       int
		score    float64
		bestRank int
	}
	ranked := make([]rankedCandidate, 0, len(scores))
	for id, score := range scores {
		ranked = append(ranked, rankedCandidate{id: id, score: score, bestRank: bestRanks[id]})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			if ranked[i].bestRank == ranked[j].bestRank {
				return ranked[i].id < ranked[j].id
			}
			return ranked[i].bestRank < ranked[j].bestRank
		}
		return ranked[i].score > ranked[j].score
	})

	ranks := make(map[int]int, len(ranked))
	for idx, candidate := range ranked {
		ranks[candidate.id] = idx + 1
	}
	return ranks
}

func diversifyRankedSearchCandidates(candidates []Snippet, limit int) []Snippet {
	if len(candidates) <= limit {
		return candidates
	}

	selected := make([]Snippet, 0, limit)
	deferred := make([]Snippet, 0, len(candidates))
	seenPath := map[string]bool{}
	for _, candidate := range candidates {
		path := normalizeIndexedPath(candidate.Path)
		if path == "" {
			selected = append(selected, candidate)
		} else if seenPath[path] {
			deferred = append(deferred, candidate)
			continue
		} else {
			seenPath[path] = true
			selected = append(selected, candidate)
		}
		if len(selected) >= limit {
			return selected
		}
	}

	for _, candidate := range deferred {
		selected = append(selected, candidate)
		if len(selected) >= limit {
			break
		}
	}
	return selected
}

func topCandidateIDByBaseScore(candidates []Snippet, baseScores []float64, primaryCandidateIDs map[int]bool) int {
	protectedID := 0
	bestScore := 0.0
	for idx, candidate := range candidates {
		if !primaryCandidateIDs[candidate.ID] {
			continue
		}
		if protectedID == 0 || baseScores[idx] < bestScore {
			protectedID = candidate.ID
			bestScore = baseScores[idx]
		}
	}
	return protectedID
}

func searchCandidateLimit(limit int) int {
	return min(max(defaultSearchCandidateLimit, limit*20), maxSearchCandidateLimit)
}

func looksLikeCodeContext(prompt string) bool {
	if strings.Contains(prompt, "\n") {
		return true
	}

	punctuationCount := 0
	for _, char := range prompt {
		switch char {
		case '(', ')', '{', '}', '[', ']', '.', ':', ';', '=', ',', '"', '\'', '`':
			punctuationCount++
		}
	}
	return punctuationCount >= 2
}

func candidateIDSet(candidates []Snippet) map[int]bool {
	ids := make(map[int]bool, len(candidates))
	for _, candidate := range candidates {
		ids[candidate.ID] = true
	}
	return ids
}

func candidateIDRankMap(candidates []Snippet) map[int]int {
	ranks := make(map[int]int, len(candidates))
	for idx, candidate := range candidates {
		if candidate.ID == 0 {
			continue
		}
		if _, exists := ranks[candidate.ID]; exists {
			continue
		}
		ranks[candidate.ID] = idx + 1
	}
	return ranks
}

func candidateValueRankMap(candidates []Snippet, values []float64, higherIsBetter bool) map[int]int {
	if len(candidates) == 0 || len(candidates) != len(values) {
		return nil
	}

	type rankedValue struct {
		id       int
		original int
		value    float64
	}
	ranked := make([]rankedValue, 0, len(candidates))
	for idx, candidate := range candidates {
		if candidate.ID == 0 || values[idx] == 0 {
			continue
		}
		ranked = append(ranked, rankedValue{id: candidate.ID, original: idx, value: values[idx]})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].value == ranked[j].value {
			return ranked[i].original < ranked[j].original
		}
		if higherIsBetter {
			return ranked[i].value > ranked[j].value
		}
		return ranked[i].value < ranked[j].value
	})

	ranks := make(map[int]int, len(ranked))
	for idx, item := range ranked {
		if _, exists := ranks[item.id]; exists {
			continue
		}
		ranks[item.id] = idx + 1
	}
	return ranks
}

func appendSnippetRows(candidates []Snippet, rows *sql.Rows) ([]Snippet, error) {
	defer rows.Close()
	for rows.Next() {
		var s Snippet
		if err := rows.Scan(&s.ID, &s.Language, &s.Topic, &s.Content, &s.Path, &s.StartLine, &s.EndLine, &s.ContentHash, &s.SourceMTime); err != nil {
			return nil, err
		}
		candidates = append(candidates, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func appendFTSPathCandidates(db *sql.DB, tokenPaths [][]string, detectedLang string, candidates []Snippet, ranks map[int]int, skipQueries map[string]bool, candidateLimit int) ([]Snippet, map[int]int, error) {
	if len(tokenPaths) == 0 || len(candidates) >= candidateLimit {
		return candidates, ranks, nil
	}
	if ranks == nil {
		ranks = map[int]int{}
	}
	if skipQueries == nil {
		skipQueries = map[string]bool{}
	}

	seen := candidateIDSet(candidates)
	for _, tokens := range tokenPaths {
		if len(candidates) >= candidateLimit {
			break
		}
		ftsQuery := snippetFTSQuery(tokens, detectedLang)
		if ftsQuery == "" || skipQueries[ftsQuery] {
			continue
		}
		skipQueries[ftsQuery] = true

		rows, err := queryKnowledgeFTS(db, ftsQuery, candidateLimit)
		if err != nil {
			return nil, nil, err
		}
		pathCandidates, err := appendSnippetRows(nil, rows)
		if err != nil {
			return nil, nil, err
		}
		for idx, candidate := range pathCandidates {
			rank := idx + 1
			if ranks[candidate.ID] == 0 || rank < ranks[candidate.ID] {
				ranks[candidate.ID] = rank
			}
			if seen[candidate.ID] {
				continue
			}
			seen[candidate.ID] = true
			candidates = append(candidates, candidate)
			if len(candidates) >= candidateLimit {
				break
			}
		}
	}
	return candidates, ranks, nil
}

func backfillSearchCandidates(db *sql.DB, candidates []Snippet, candidateLimit int) ([]Snippet, error) {
	seen := make(map[int]bool, len(candidates))
	for _, candidate := range candidates {
		seen[candidate.ID] = true
	}

	rows, err := db.Query(`
		SELECT id, language, topic, content, path, start_line, end_line, content_hash, source_mtime
		FROM knowledge
		ORDER BY id ASC
		LIMIT ?`, candidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var s Snippet
		if err := rows.Scan(&s.ID, &s.Language, &s.Topic, &s.Content, &s.Path, &s.StartLine, &s.EndLine, &s.ContentHash, &s.SourceMTime); err != nil {
			return nil, err
		}
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		candidates = append(candidates, s)
		if len(candidates) >= candidateLimit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func appendSmallCorpusSearchCandidates(db *sql.DB, candidates []Snippet, candidateLimit int) ([]Snippet, error) {
	rowCount, err := tableRowCount(db, "knowledge")
	if err != nil {
		return nil, err
	}
	if rowCount == 0 || rowCount > candidateLimit || len(candidates) >= rowCount {
		return candidates, nil
	}
	return backfillSearchCandidates(db, candidates, candidateLimit)
}

func appendStructuredSearchCandidates(db *sql.DB, prompt string, candidates []Snippet, candidateLimit int) ([]Snippet, error) {
	paths, err := structuredCandidatePaths(db, prompt, min(candidateLimit, 60))
	if err != nil {
		return nil, err
	}
	return appendStructuredSearchCandidatesForPaths(db, paths, candidates, candidateLimit)
}

func appendStructuredSearchCandidatesForPaths(db *sql.DB, paths []string, candidates []Snippet, candidateLimit int) ([]Snippet, error) {
	if len(paths) == 0 {
		return candidates, nil
	}

	seen := candidateIDSet(candidates)
	for _, path := range paths {
		if len(candidates) >= candidateLimit {
			break
		}
		var err error
		candidates, err = appendKnowledgeForPath(db, candidates, seen, path, candidateLimit)
		if err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func structuredSearchPrompt(prompt string, plannedPrompt string, primaryWords []string, expandedFallback bool) string {
	structuredPrompt := strings.TrimSpace(plannedPrompt)
	if structuredPrompt == "" {
		structuredPrompt = strings.Join(primaryWords, " ")
	}
	if expandedFallback || structuredPrompt == "" {
		return prompt
	}
	return structuredPrompt
}

func rankedPathMap(paths []string) map[string]int {
	ranks := make(map[string]int, len(paths))
	for idx, path := range paths {
		path = normalizeIndexedPath(path)
		if path == "" {
			continue
		}
		if _, exists := ranks[path]; exists {
			continue
		}
		ranks[path] = idx + 1
	}
	return ranks
}

func structuredCandidatePaths(db *sql.DB, prompt string, limit int) ([]string, error) {
	limit = normalizeResultLimit(limit, 20)
	seen := map[string]bool{}
	paths := make([]string, 0, limit)
	addPath := func(path string) {
		path = normalizeIndexedPath(path)
		if path == "" || seen[path] || len(paths) >= limit {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}

	symbols, err := SearchSymbols(db, prompt, limit)
	if err != nil {
		return nil, err
	}
	for _, symbol := range symbols {
		addPath(symbol.Path)
	}

	refs, err := SearchSymbolReferences(db, prompt, limit)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		addPath(ref.Path)
	}

	imports, err := SearchImports(db, prompt, limit)
	if err != nil {
		return nil, err
	}
	for _, ref := range imports {
		addPath(ref.TargetPath)
		addPath(ref.Path)
	}
	return paths, nil
}

func appendKnowledgeForPath(db *sql.DB, candidates []Snippet, seen map[int]bool, path string, candidateLimit int) ([]Snippet, error) {
	rows, err := db.Query(`
		SELECT id, language, topic, content, path, start_line, end_line, content_hash, source_mtime
		FROM knowledge
		WHERE path = ?
		ORDER BY start_line ASC, id ASC
		LIMIT ?`, path, candidateLimit-len(candidates))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var s Snippet
		if err := rows.Scan(&s.ID, &s.Language, &s.Topic, &s.Content, &s.Path, &s.StartLine, &s.EndLine, &s.ContentHash, &s.SourceMTime); err != nil {
			return nil, err
		}
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		candidates = append(candidates, s)
		if len(candidates) >= candidateLimit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func planRetrievalQuery(prompt string) retrievalQueryPlan {
	primaryTokens := primarySearchTokens(prompt)
	expandedTokens := searchTokens(prompt)
	codeTokens := codeContextSearchTokens(prompt)

	if len(codeTokens) == 0 || !looksLikeCodeContext(prompt) {
		rankingTokens := mergeTokenLists(primaryTokens, expandedTokens)
		if len(rankingTokens) == 0 {
			rankingTokens = firstTokenList(primaryTokens, expandedTokens)
		}

		synRankingTokens := expandSynonymTokens(rankingTokens)
		synPrimaryTokens := expandSynonymTokens(primaryTokens)

		return retrievalQueryPlan{
			FTSTokens:        limitSearchTokens(synPrimaryTokens, 32),
			FTSPaths:         retrievalFTSPaths(synPrimaryTokens, rankingTokens, synRankingTokens),
			RankingTokens:    limitSearchTokens(synRankingTokens, 48),
			StructuredPrompt: strings.Join(limitSearchTokens(synRankingTokens, 24), " "),
		}
	}

	ftsTokens := mergeTokenLists(filterLowSignalSearchTokens(primaryTokens), codeTokens)
	if len(ftsTokens) == 0 {
		ftsTokens = primaryTokens
	}

	rankingTokens := mergeTokenLists(codeTokens, filterLowSignalSearchTokens(expandedTokens))
	if len(rankingTokens) == 0 {
		rankingTokens = firstTokenList(primaryTokens, expandedTokens)
	}

	return retrievalQueryPlan{
		FTSTokens:        limitSearchTokens(ftsTokens, 32),
		FTSPaths:         retrievalFTSPaths(primaryTokens, codeTokens, rankingTokens),
		RankingTokens:    limitSearchTokens(rankingTokens, 48),
		StructuredPrompt: strings.Join(limitSearchTokens(rankingTokens, 24), " "),
	}
}

func retrievalFTSPaths(tokenLists ...[]string) [][]string {
	seen := map[string]bool{}
	var paths [][]string
	for _, tokens := range tokenLists {
		path := limitSearchTokens(retrievalPathTokens(tokens), 32)
		if len(path) == 0 {
			continue
		}
		key := strings.Join(path, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		paths = append(paths, path)
	}
	return paths
}

func retrievalPathTokens(tokens []string) []string {
	unique := uniqueStrings(tokens)
	var filtered []string
	for _, token := range unique {
		if meaningfulCodeSearchToken(token) || isLanguageQueryToken(token) {
			filtered = append(filtered, token)
		}
	}
	if len(filtered) == 0 {
		return unique
	}
	return filtered
}

func codeContextSearchTokens(prompt string) []string {
	var tokens []string
	for _, raw := range identifierFields(prompt) {
		for _, token := range searchTokens(raw) {
			if meaningfulCodeSearchToken(token) {
				tokens = append(tokens, token)
			}
		}
	}
	return uniqueStrings(tokens)
}

func filterLowSignalSearchTokens(tokens []string) []string {
	var filtered []string
	for _, token := range tokens {
		if meaningfulCodeSearchToken(token) {
			filtered = append(filtered, token)
		}
	}
	if len(filtered) == 0 {
		return tokens
	}
	return uniqueStrings(filtered)
}

func meaningfulCodeSearchToken(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if len(token) < 2 || codeSearchStopTokens[token] {
		return false
	}
	onlyDigits := true
	for _, char := range token {
		if !isDigitASCII(char) {
			onlyDigits = false
			break
		}
	}
	return !onlyDigits
}

func mergeTokenLists(lists ...[]string) []string {
	var merged []string
	for _, list := range lists {
		merged = append(merged, list...)
	}
	return uniqueStrings(merged)
}

func firstTokenList(lists ...[]string) []string {
	for _, list := range lists {
		if len(list) > 0 {
			return list
		}
	}
	return nil
}

func limitSearchTokens(tokens []string, limit int) []string {
	if limit <= 0 || len(tokens) <= limit {
		return tokens
	}
	return tokens[:limit]
}

var codeSearchStopTokens = stringSet(
	"and",
	"as",
	"async",
	"await",
	"break",
	"case",
	"catch",
	"class",
	"const",
	"continue",
	"def",
	"else",
	"elif",
	"enum",
	"except",
	"false",
	"final",
	"finally",
	"fn",
	"for",
	"from",
	"func",
	"function",
	"if",
	"impl",
	"import",
	"in",
	"interface",
	"is",
	"let",
	"match",
	"namespace",
	"new",
	"nil",
	"none",
	"not",
	"null",
	"or",
	"package",
	"pass",
	"private",
	"protected",
	"public",
	"raise",
	"return",
	"self",
	"static",
	"struct",
	"switch",
	"this",
	"throw",
	"throws",
	"true",
	"try",
	"type",
	"using",
	"var",
	"void",
	"where",
	"while",
	"with",
)

func searchTokens(input string) []string {
	var tokens []string
	for _, raw := range identifierFields(input) {
		tokens = appendIdentifierTokens(tokens, raw)
	}
	return uniqueStrings(tokens)
}

func primarySearchTokens(input string) []string {
	var tokens []string
	for _, token := range strings.FieldsFunc(strings.ToLower(input), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if len(token) > 1 {
			tokens = append(tokens, token)
		}
	}
	return uniqueStrings(tokens)
}

func snippetFTSQuery(tokens []string, detectedLang string) string {
	var ftsTokens []string
	for _, token := range tokens {
		if detectedLang != "" && isLanguageQueryToken(token) {
			continue
		}
		if len(token) > 1 {
			ftsTokens = append(ftsTokens, token)
		}
	}
	if len(ftsTokens) == 0 {
		for _, token := range tokens {
			if len(token) > 1 {
				ftsTokens = append(ftsTokens, token)
			}
		}
	}
	return strings.Join(uniqueStrings(ftsTokens), " OR ")
}

func identifierFields(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		return !isIdentifierRune(r)
	})
}

func appendIdentifierTokens(tokens []string, raw string) []string {
	raw = strings.Trim(raw, "_")
	if raw == "" {
		return tokens
	}

	lowerRaw := strings.ToLower(raw)
	if len(lowerRaw) > 1 {
		tokens = append(tokens, lowerRaw)
	}
	if strings.Contains(lowerRaw, "_") {
		compact := strings.ReplaceAll(lowerRaw, "_", "")
		if len(compact) > 1 {
			tokens = append(tokens, compact)
		}
	}
	for _, part := range splitIdentifierParts(raw) {
		part = strings.ToLower(part)
		if len(part) > 1 {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

func splitIdentifierParts(value string) []string {
	segments := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_'
	})
	var parts []string
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		start := 0
		for idx := 1; idx < len(segment); idx++ {
			prev := rune(segment[idx-1])
			cur := rune(segment[idx])
			next := rune(0)
			if idx+1 < len(segment) {
				next = rune(segment[idx+1])
			}
			if identifierBoundary(prev, cur, next) {
				parts = append(parts, segment[start:idx])
				start = idx
			}
		}
		parts = append(parts, segment[start:])
	}
	return parts
}

func identifierBoundary(prev, cur, next rune) bool {
	if isUpperASCII(cur) && (isLowerASCII(prev) || isDigitASCII(prev)) {
		return true
	}
	if isUpperASCII(prev) && isUpperASCII(cur) && isLowerASCII(next) {
		return true
	}
	if isDigitASCII(cur) != isDigitASCII(prev) {
		return true
	}
	return false
}

func isIdentifierRune(r rune) bool {
	return isLetterASCII(r) || isDigitASCII(r) || r == '_'
}

func isLetterASCII(r rune) bool {
	return isLowerASCII(r) || isUpperASCII(r)
}

func isLowerASCII(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isUpperASCII(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isDigitASCII(r rune) bool {
	return r >= '0' && r <= '9'
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

func pathTokenBoost(queryTokens []string, path string) float64 {
	if len(queryTokens) == 0 || path == "" {
		return 0
	}
	path = strings.ToLower(normalizeIndexedPath(path))
	delimiterFunc := func(r rune) bool {
		return r == '/' || r == '\\' || r == '_' || r == '-' || r == '.'
	}
	segments := strings.FieldsFunc(path, delimiterFunc)
	if len(segments) == 0 {
		return 0
	}
	segmentSet := make(map[string]bool, len(segments))
	for _, seg := range segments {
		if len(seg) > 1 && seg != "src" && seg != "internal" && seg != "pkg" && seg != "tests" && seg != "test" && seg != "dist" && seg != "build" {
			segmentSet[seg] = true
		}
	}
	matches := 0
	for _, token := range queryTokens {
		token = strings.ToLower(token)
		if len(token) > 1 {
			if segmentSet[token] {
				matches++
			} else {
				for seg := range segmentSet {
					if strings.Contains(seg, token) {
						matches++
						break
					}
				}
			}
		}
	}
	if matches == 0 {
		return 0
	}
	score := 0.015 * float64(matches)
	if score > 0.05 {
		return 0.05
	}
	return score
}

type retrievalScoreComponents struct {
	LexicalBoost    float64
	BM25Boost       float64
	StructuredBoost float64
	PathTokenBoost  float64
	LanguageBoost   float64
	StructureBoost  float64
	TopicPenalty    float64
}

func candidateScoreComponents(queryTokens []string, tokenWeights map[string]float64, bm25Boost float64, structuredBoost float64, snippet Snippet, detectedLang, structureIntent string, structureWeight float64) retrievalScoreComponents {
	components := retrievalScoreComponents{
		LexicalBoost:    lexicalBoost(queryTokens, tokenWeights, snippet),
		BM25Boost:       bm25Boost,
		StructuredBoost: structuredBoost,
		PathTokenBoost:  pathTokenBoost(queryTokens, snippet.Path),
		StructureBoost:  structureBoost(structureIntent, snippet.Content, structureWeight),
		TopicPenalty:    topicTypePenalty(queryTokens, snippet.Topic),
	}
	if detectedLang != "" && NormalizeLanguage(snippet.Language) == detectedLang {
		components.LanguageBoost = 0.08
	}
	return components
}

func (components retrievalScoreComponents) totalBoost() float64 {
	return components.LexicalBoost + components.BM25Boost + components.StructuredBoost + components.PathTokenBoost + components.LanguageBoost + components.StructureBoost
}

func (components retrievalScoreComponents) score(qnd float64) float64 {
	return qnd - components.totalBoost() + components.TopicPenalty
}

func relevanceScore(qnd float64, queryTokens []string, tokenWeights map[string]float64, bm25Boost float64, structuredBoost float64, snippet Snippet, detectedLang, structureIntent string, structureWeight float64) float64 {
	return candidateScoreComponents(queryTokens, tokenWeights, bm25Boost, structuredBoost, snippet, detectedLang, structureIntent, structureWeight).score(qnd)
}

func candidateBM25Boosts(queryTokens []string, candidates []Snippet) []float64 {
	boosts := make([]float64, len(candidates))
	if len(queryTokens) == 0 || len(candidates) == 0 {
		return boosts
	}

	docTokens := candidateDocumentTokens(candidates)
	docFreq := make(map[string]int, len(queryTokens))
	querySet := stringSet(queryTokens...)
	totalLength := 0
	for _, tokens := range docTokens {
		totalLength += len(tokens)

		seen := map[string]bool{}
		for _, token := range tokens {
			if !querySet[token] || seen[token] {
				continue
			}
			seen[token] = true
			docFreq[token]++
		}
	}
	if totalLength == 0 {
		return boosts
	}

	avgLength := float64(totalLength) / float64(len(candidates))
	if avgLength == 0 {
		return boosts
	}

	const k1 = 1.2
	const b = 0.75
	maxScore := 0.0
	rawScores := make([]float64, len(candidates))
	for idx, tokens := range docTokens {
		if len(tokens) == 0 {
			continue
		}
		counts := make(map[string]int, len(tokens))
		for _, token := range tokens {
			counts[token]++
		}

		docLength := float64(len(tokens))
		for _, token := range queryTokens {
			freq := float64(counts[token])
			if freq == 0 {
				continue
			}
			df := float64(docFreq[token])
			if df == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(candidates))-df+0.5)/(df+0.5))
			denom := freq + k1*(1-b+b*docLength/avgLength)
			rawScores[idx] += idf * freq * (k1 + 1) / denom
		}
		if rawScores[idx] > maxScore {
			maxScore = rawScores[idx]
		}
	}
	if maxScore == 0 {
		return boosts
	}
	for idx, score := range rawScores {
		boosts[idx] = lexicalBM25Blend * score / maxScore
	}
	return boosts
}

type candidateBM25FDocument struct {
	fields [][]string
}

func candidateBM25FBoosts(queryTokens []string, candidates []Snippet) []float64 {
	boosts := make([]float64, len(candidates))
	if len(queryTokens) == 0 || len(candidates) == 0 {
		return boosts
	}

	documents := candidateBM25FDocuments(candidates)
	if len(documents) == 0 {
		return boosts
	}

	fieldWeights := []float64{3.0, 1.4, 2.5, 1.0}
	fieldB := []float64{0.20, 0.40, 0.55, 0.75}
	avgFieldLens := make([]float64, len(fieldWeights))
	for _, document := range documents {
		for fieldIdx, tokens := range document.fields {
			avgFieldLens[fieldIdx] += float64(len(tokens))
		}
	}
	for idx := range avgFieldLens {
		avgFieldLens[idx] /= float64(len(documents))
	}

	querySet := stringSet(queryTokens...)
	docFreq := make(map[string]int, len(querySet))
	for _, document := range documents {
		seen := map[string]bool{}
		for _, field := range document.fields {
			for _, token := range field {
				if !querySet[token] || seen[token] {
					continue
				}
				seen[token] = true
				docFreq[token]++
			}
		}
	}

	const k1 = 1.2
	rawScores := make([]float64, len(candidates))
	maxScore := 0.0
	for docIdx, document := range documents {
		fieldCounts := make([]map[string]int, len(document.fields))
		for fieldIdx, field := range document.fields {
			counts := make(map[string]int, len(field))
			for _, token := range field {
				counts[token]++
			}
			fieldCounts[fieldIdx] = counts
		}

		for _, token := range queryTokens {
			df := float64(docFreq[token])
			if df == 0 {
				continue
			}
			weightedTF := 0.0
			for fieldIdx := range document.fields {
				freq := float64(fieldCounts[fieldIdx][token])
				if freq == 0 || avgFieldLens[fieldIdx] == 0 {
					continue
				}
				fieldLen := float64(len(document.fields[fieldIdx]))
				norm := 1 - fieldB[fieldIdx] + fieldB[fieldIdx]*fieldLen/avgFieldLens[fieldIdx]
				if norm <= 0 {
					norm = 1
				}
				weightedTF += fieldWeights[fieldIdx] * freq / norm
			}
			if weightedTF == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(candidates))-df+0.5)/(df+0.5))
			rawScores[docIdx] += idf * (weightedTF * (k1 + 1)) / (weightedTF + k1)
		}
		if rawScores[docIdx] > maxScore {
			maxScore = rawScores[docIdx]
		}
	}
	if maxScore == 0 {
		return boosts
	}
	for idx, score := range rawScores {
		boosts[idx] = lexicalBM25FBlend * score / maxScore
	}
	return boosts
}

func candidateBM25FDocuments(candidates []Snippet) []candidateBM25FDocument {
	documents := make([]candidateBM25FDocument, len(candidates))
	for idx, candidate := range candidates {
		documents[idx] = candidateBM25FDocument{
			fields: [][]string{
				documentSearchTokens(candidate.Path),
				documentSearchTokens(candidate.Topic),
				codeDeclarationTokens(candidate.Content),
				documentSearchTokens(candidate.Content),
			},
		}
	}
	return documents
}

func codeDeclarationTokens(content string) []string {
	var tokens []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !looksLikeDeclarationLine(line) {
			continue
		}
		tokens = append(tokens, documentSearchTokens(line)...)
	}
	return tokens
}

func looksLikeDeclarationLine(line string) bool {
	if line == "" ||
		strings.HasPrefix(line, "#") ||
		strings.HasPrefix(line, "//") ||
		strings.HasPrefix(line, "/*") ||
		strings.HasPrefix(line, "*") ||
		strings.HasPrefix(line, "import ") ||
		strings.HasPrefix(line, "from ") ||
		strings.HasPrefix(line, "package ") {
		return false
	}
	lower := strings.ToLower(line)
	if startsWithAny(lower, "if ", "for ", "while ", "switch ", "catch ", "return ", "throw ") {
		return false
	}
	if startsWithAny(lower,
		"def ",
		"class ",
		"func ",
		"function ",
		"interface ",
		"struct ",
		"enum ",
		"record ",
		"type ",
		"const ",
		"let ",
		"var ",
		"public ",
		"private ",
		"protected ",
		"static ",
		"final ",
	) {
		return true
	}
	return strings.Contains(line, "(") && strings.Contains(line, ")") &&
		(strings.Contains(line, "{") || strings.HasSuffix(line, ":"))
}

func candidateJaccardRankMap(queryTokens []string, candidates []Snippet) map[int]int {
	if len(queryTokens) == 0 || len(candidates) == 0 {
		return nil
	}

	querySet := stringSet(queryTokens...)
	if len(querySet) == 0 {
		return nil
	}

	docTokens := candidateDocumentTokens(candidates)
	scores := make([]float64, len(candidates))
	for idx, tokens := range docTokens {
		docSet := stringSet(tokens...)
		if len(docSet) == 0 {
			continue
		}
		intersection := 0
		for token := range querySet {
			if docSet[token] {
				intersection++
			}
		}
		if intersection == 0 {
			continue
		}
		union := len(querySet)
		for token := range docSet {
			if !querySet[token] {
				union++
			}
		}
		if union > 0 {
			scores[idx] = float64(intersection) / float64(union)
		}
	}
	return candidateValueRankMap(candidates, scores, true)
}

func candidateExactIdentifierBoosts(prompt string, candidates []Snippet) []float64 {
	boosts := make([]float64, len(candidates))
	if !promptHasImportContext(prompt) {
		return boosts
	}
	queryTokens := exactIdentifierQueryTokens(prompt)
	if len(queryTokens) == 0 || len(candidates) == 0 {
		return boosts
	}

	docTokens := candidateDocumentTokens(candidates)
	docFreq := make(map[string]int, len(queryTokens))
	querySet := stringSet(queryTokens...)
	for _, tokens := range docTokens {
		seen := map[string]bool{}
		for _, token := range tokens {
			if !querySet[token] || seen[token] {
				continue
			}
			seen[token] = true
			docFreq[token]++
		}
	}

	rawScores := make([]float64, len(candidates))
	maxScore := 0.0
	for idx, tokens := range docTokens {
		seen := map[string]bool{}
		for _, token := range tokens {
			if !querySet[token] || seen[token] {
				continue
			}
			seen[token] = true
			df := docFreq[token]
			if df == 0 {
				continue
			}
			rawScores[idx] += math.Log(1 + float64(len(candidates)+1)/float64(df+1))
		}
		if rawScores[idx] > maxScore {
			maxScore = rawScores[idx]
		}
	}
	if maxScore == 0 {
		return boosts
	}
	for idx, score := range rawScores {
		boosts[idx] = exactIdentifierBlend * score / maxScore
	}
	return boosts
}

func promptHasImportContext(prompt string) bool {
	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if startsWithAny(trimmed,
			"import ",
			"from ",
			"using ",
			"use ",
			"require ",
			"require(",
			"require_relative ",
			"#include ",
		) {
			return true
		}
		if strings.Contains(trimmed, " require(") || strings.Contains(trimmed, "= require(") {
			return true
		}
	}
	return false
}

func exactIdentifierQueryTokens(prompt string) []string {
	var tokens []string
	for _, raw := range identifierFields(prompt) {
		identifier := normalizeStructuralIdentifier(raw)
		if !specificStructuralIdentifier(identifier) || isLanguageQueryToken(identifier) {
			continue
		}
		tokens = append(tokens, identifier)
		compact := strings.ReplaceAll(identifier, "_", "")
		if compact != identifier && specificStructuralIdentifier(compact) {
			tokens = append(tokens, compact)
		}
	}
	return uniqueStrings(tokens)
}

func candidateDocumentTokens(candidates []Snippet) [][]string {
	tokens := make([][]string, len(candidates))
	for idx, candidate := range candidates {
		tokens[idx] = documentSearchTokens(candidate.Topic + " " + candidate.Content)
	}
	return tokens
}

func candidateStructuredPathBoosts(candidates []Snippet, pathRanks map[string]int, primaryCandidateIDs map[int]bool) []float64 {
	boosts := make([]float64, len(candidates))
	if len(candidates) == 0 || len(pathRanks) == 0 {
		return boosts
	}

	for idx, candidate := range candidates {
		if primaryCandidateIDs[candidate.ID] {
			continue
		}
		rank := pathRanks[normalizeIndexedPath(candidate.Path)]
		if rank == 0 {
			continue
		}
		boosts[idx] = structuredPathBlend / math.Sqrt(float64(rank))
	}
	return boosts
}

func candidateStructuralRerankBoosts(db *sql.DB, prompt string, candidates []Snippet) ([]float64, map[int]int, error) {
	boosts := make([]float64, len(candidates))
	if db == nil || strings.TrimSpace(prompt) == "" || len(candidates) == 0 || !allowStructuralRerank(prompt) {
		return boosts, nil, nil
	}
	queryIdentifiers := structuralQueryIdentifiers(prompt)
	if len(queryIdentifiers) == 0 {
		return boosts, nil, nil
	}

	pathSignals := map[string]float64{}
	pathBestRanks := map[string]int{}
	addPathSignal := func(path string, rank int, weight float64) {
		path = normalizeIndexedPath(path)
		if path == "" || rank <= 0 || weight <= 0 {
			return
		}
		pathSignals[path] += weight / (rankFusionK + float64(rank))
		if pathBestRanks[path] == 0 || rank < pathBestRanks[path] {
			pathBestRanks[path] = rank
		}
	}

	symbols, err := SearchSymbols(db, prompt, structuralRerankMetadataLimit)
	if err != nil {
		return nil, nil, err
	}
	for idx, symbol := range symbols {
		if !structuralIdentifierMatches(symbol.Name, queryIdentifiers) {
			continue
		}
		addPathSignal(symbol.Path, idx+1, structuralSymbolWeight(symbol))
	}

	refs, err := SearchSymbolReferences(db, prompt, structuralRerankMetadataLimit)
	if err != nil {
		return nil, nil, err
	}
	for idx, ref := range refs {
		if !structuralIdentifierMatches(ref.Name, queryIdentifiers) {
			continue
		}
		addPathSignal(ref.Path, idx+1, 0.85)
	}

	imports, err := SearchImports(db, prompt, structuralRerankMetadataLimit)
	if err != nil {
		return nil, nil, err
	}
	for idx, ref := range imports {
		if !structuralImportMatches(ref, queryIdentifiers) {
			continue
		}
		rank := idx + 1
		addPathSignal(ref.TargetPath, rank, 1.00)
		addPathSignal(ref.Path, rank, 0.70)
	}

	maxSignal := 0.0
	for _, signal := range pathSignals {
		if signal > maxSignal {
			maxSignal = signal
		}
	}
	if maxSignal == 0 {
		return boosts, nil, nil
	}

	type structuralCandidate struct {
		id       int
		original int
		path     string
		signal   float64
		bestRank int
	}
	ranked := make([]structuralCandidate, 0, len(candidates))
	for idx, candidate := range candidates {
		path := normalizeIndexedPath(candidate.Path)
		signal := pathSignals[path]
		if signal == 0 {
			continue
		}
		boosts[idx] = structuralRerankBlend * signal / maxSignal
		if candidate.ID != 0 {
			ranked = append(ranked, structuralCandidate{
				id:       candidate.ID,
				original: idx,
				path:     path,
				signal:   signal,
				bestRank: pathBestRanks[path],
			})
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].signal == ranked[j].signal {
			if ranked[i].bestRank == ranked[j].bestRank {
				if ranked[i].path == ranked[j].path {
					return ranked[i].original < ranked[j].original
				}
				return ranked[i].path < ranked[j].path
			}
			return ranked[i].bestRank < ranked[j].bestRank
		}
		return ranked[i].signal > ranked[j].signal
	})

	ranks := make(map[int]int, len(ranked))
	for idx, candidate := range ranked {
		if _, exists := ranks[candidate.id]; exists {
			continue
		}
		ranks[candidate.id] = idx + 1
	}
	return boosts, ranks, nil
}

func allowStructuralRerank(prompt string) bool {
	if looksLikeCodeContext(prompt) {
		return false
	}
	nonEmptyLines := 0
	for _, line := range strings.Split(prompt, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmptyLines++
		if nonEmptyLines > 1 {
			return false
		}
	}
	return true
}

func structuralQueryIdentifiers(prompt string) map[string]bool {
	identifiers := map[string]bool{}
	for _, raw := range identifierFields(prompt) {
		identifier := normalizeStructuralIdentifier(raw)
		if !specificStructuralIdentifier(identifier) {
			continue
		}
		identifiers[identifier] = true
		if compact := strings.ReplaceAll(identifier, "_", ""); compact != identifier && specificStructuralIdentifier(compact) {
			identifiers[compact] = true
		}
	}
	return identifiers
}

func structuralIdentifierMatches(name string, identifiers map[string]bool) bool {
	identifier := normalizeStructuralIdentifier(name)
	if identifiers[identifier] {
		return true
	}
	compact := strings.ReplaceAll(identifier, "_", "")
	return compact != identifier && identifiers[compact]
}

func structuralImportMatches(ref ImportReference, identifiers map[string]bool) bool {
	values := []string{
		ref.ImportPath,
		ref.Alias,
		ref.TargetPath,
		ref.Context,
	}
	for _, value := range values {
		for _, raw := range identifierFields(value) {
			if structuralIdentifierMatches(raw, identifiers) {
				return true
			}
		}
	}
	return false
}

func normalizeStructuralIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, "_")
	return value
}

func specificStructuralIdentifier(identifier string) bool {
	if identifier == "" || !meaningfulCodeSearchToken(identifier) || commonSymbolReferenceName(identifier) {
		return false
	}
	return len(identifier) >= 5 || strings.Contains(identifier, "_")
}

func structuralSymbolWeight(symbol Symbol) float64 {
	switch symbol.Kind {
	case "class", "type", "interface", "struct":
		return 1.25
	case "function", "method", "constructor":
		return 1.15
	default:
		return 0.95
	}
}

func documentSearchTokens(input string) []string {
	var tokens []string
	for _, raw := range identifierFields(input) {
		tokens = appendIdentifierTokens(tokens, raw)
	}
	return tokens
}

func queryStructureIntent(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
			continue
		}
		if strings.HasPrefix(trimmed, "class ") {
			return "class"
		}
		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ") {
			if leadingIndentWidth(line) > 0 {
				return "method"
			}
			return "function"
		}
	}
	return ""
}

func queryStructureBoostWeight(prompt string, queryTokens []string) float64 {
	if queryStructureIntent(prompt) == "" || declarationFollowedByDocstring(prompt) {
		return 0
	}
	if len(queryTokens) >= 4 {
		return 0.10
	}
	return 0.05
}

func declarationFollowedByDocstring(prompt string) bool {
	lines := strings.Split(prompt, "\n")
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
			continue
		}
		if !(strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ") || strings.HasPrefix(trimmed, "class ")) {
			return false
		}
		for _, following := range lines[idx+1:] {
			next := strings.TrimSpace(following)
			if next == "" {
				continue
			}
			return strings.HasPrefix(next, `"""`) || strings.HasPrefix(next, `'''`)
		}
		return false
	}
	return false
}

func snippetLeadingStructure(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
			continue
		}
		if strings.HasPrefix(trimmed, "class ") {
			return "class"
		}
		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ") {
			if leadingIndentWidth(line) > 0 {
				return "method"
			}
			return "function"
		}
		return ""
	}
	return ""
}

func structureBoost(intent, content string, weight float64) float64 {
	if intent == "" || weight <= 0 {
		return 0
	}
	structure := snippetLeadingStructure(content)
	if structure == "" {
		return 0
	}
	if structure == intent {
		return weight
	}
	if (intent == "function" || intent == "method") && (structure == "function" || structure == "method") {
		return weight * 0.6
	}
	return 0
}

func queryTokenWeights(queryTokens []string, candidates []Snippet) map[string]float64 {
	weights := make(map[string]float64, len(queryTokens))
	if len(queryTokens) == 0 || len(candidates) == 0 {
		return weights
	}

	for _, token := range queryTokens {
		df := 0
		for _, candidate := range candidates {
			haystack := strings.ToLower(candidate.Topic + " " + candidate.Content)
			if strings.Contains(haystack, token) {
				df++
			}
		}
		if df == 0 {
			weights[token] = 1
			continue
		}
		idf := math.Log(float64(len(candidates)+1) / float64(df+1))
		weights[token] = 1 + lexicalIDFBlend*minFloat(1.5, idf)
	}
	return weights
}

func lexicalBoost(queryTokens []string, tokenWeights map[string]float64, snippet Snippet) float64 {
	if len(queryTokens) == 0 {
		return 0
	}

	topic := strings.ToLower(snippet.Topic)
	content := strings.ToLower(snippet.Content)
	topicHits := 0.0
	contentHits := 0.0
	totalWeight := 0.0
	for _, token := range queryTokens {
		weight := tokenWeights[token]
		if weight == 0 {
			weight = 1
		}
		totalWeight += weight
		if strings.Contains(topic, token) {
			topicHits += weight
		}
		if strings.Contains(content, token) {
			contentHits += weight
		}
	}

	if totalWeight == 0 {
		totalWeight = float64(len(queryTokens))
	}
	boost := 0.12 * topicHits
	boost += 0.035 * contentHits
	boost += 0.20 * (topicHits + contentHits) / totalWeight
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

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func pathFromTopic(topic string) string {
	topic = strings.TrimSpace(topic)
	topic = strings.TrimPrefix(topic, "Source file: ")
	if idx := strings.Index(topic, " #chunk-"); idx >= 0 {
		topic = topic[:idx]
	}
	return strings.TrimSpace(topic)
}

var softwareSynonymMap = map[string][]string{
	"delete":       {"remove", "evict", "purge", "clear", "destroy", "discard", "erase"},
	"remove":       {"delete", "evict", "purge", "clear", "destroy", "discard", "erase"},
	"evict":        {"delete", "remove", "purge", "clear", "discard"},
	"purge":        {"delete", "remove", "evict", "clear", "discard"},
	"destroy":      {"delete", "remove", "terminate", "close", "shutdown", "kill"},
	"create":       {"build", "make", "new", "init", "setup", "generate", "produce"},
	"build":        {"create", "make", "new", "init", "setup", "generate"},
	"init":         {"create", "build", "setup", "initialize", "start", "begin"},
	"initialize":   {"create", "build", "setup", "init", "start", "begin"},
	"start":        {"init", "begin", "launch", "run", "execute"},
	"stop":         {"close", "shutdown", "terminate", "kill", "halt"},
	"close":        {"stop", "shutdown", "terminate", "finalize"},
	"shutdown":     {"stop", "close", "terminate", "kill"},
	"fetch":        {"get", "retrieve", "load", "read", "query"},
	"get":          {"fetch", "retrieve", "load", "read", "query"},
	"retrieve":     {"fetch", "get", "load", "read", "query"},
	"load":         {"fetch", "get", "retrieve", "read"},
	"save":         {"write", "store", "persist", "insert", "update"},
	"store":        {"save", "write", "persist", "insert"},
	"persist":      {"save", "write", "store", "insert"},
	"write":        {"save", "store", "persist", "insert"},
	"add":          {"insert", "push", "append", "put"},
	"insert":       {"add", "push", "append", "put"},
	"push":         {"add", "insert", "append"},
	"append":       {"add", "insert", "push"},
	"put":          {"add", "insert", "set"},
	"error":        {"fail", "failure", "exception", "panic", "err", "warn", "warning"},
	"fail":         {"error", "failure", "exception", "panic", "err"},
	"failure":      {"error", "fail", "exception", "panic", "err"},
	"err":          {"error", "fail", "failure", "exception", "panic"},
	"warn":         {"warning", "error"},
	"warning":      {"warn", "error"},
	"timeout":      {"deadline", "expiry", "expiration", "ttl"},
	"expiry":       {"timeout", "deadline", "expiration", "ttl"},
	"expiration":   {"timeout", "deadline", "expiry", "ttl"},
	"ttl":          {"timeout", "deadline", "expiry", "expiration"},
	"auth":         {"login", "signin", "authenticate", "authorize", "token", "session"},
	"login":        {"auth", "signin", "authenticate"},
	"signin":       {"auth", "login", "authenticate"},
	"authenticate": {"auth", "login", "signin", "authorize"},
	"authorize":    {"auth", "authenticate", "permission", "allow"},
}

func expandSynonymTokens(tokens []string) []string {
	var expanded []string
	seen := map[string]bool{}
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			expanded = append(expanded, t)
		}
		if syns, ok := softwareSynonymMap[strings.ToLower(t)]; ok {
			for _, syn := range syns {
				if !seen[syn] {
					seen[syn] = true
					expanded = append(expanded, syn)
				}
			}
		}
	}
	return expanded
}

func getDBDir(db *sql.DB) (string, error) {
	var seq int
	var name string
	var file string
	err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file)
	if err != nil {
		return "", err
	}
	if file == "" {
		return "", fmt.Errorf("in-memory database")
	}
	return filepath.Dir(file), nil
}

func gitRecentFiles(root string) (map[string]float64, error) {
	cmd := exec.Command("git", "-C", root, "log", "--name-only", "-n", "50", "--pretty=format:")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	weights := make(map[string]float64)
	rank := 1
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = filepath.ToSlash(line)
		if _, exists := weights[line]; !exists {
			weight := 0.05 / float64(rank)
			if weight < 0.005 {
				weight = 0.005
			}
			weights[line] = weight
			rank++
			if rank > 20 {
				break
			}
		}
	}
	return weights, nil
}

func runExternalReranker(cmdStr string, query string, snippets []Snippet) ([]Snippet, error) {
	if cmdStr == "" || len(snippets) < 2 {
		return snippets, nil
	}

	if strings.HasPrefix(cmdStr, "http://") || strings.HasPrefix(cmdStr, "https://") {
		url := cmdStr
		if !strings.HasSuffix(url, "/rerank") && !strings.Contains(url, "?") {
			url = strings.TrimSuffix(url, "/") + "/rerank"
		}

		inputStruct := struct {
			Query      string    `json:"query"`
			Candidates []Snippet `json:"candidates"`
		}{
			Query:      query,
			Candidates: snippets,
		}
		inputBytes, err := json.Marshal(inputStruct)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal reranker input: %w", err)
		}

		resp, err := http.Post(url, "application/json", bytes.NewReader(inputBytes))
		if err != nil {
			return nil, fmt.Errorf("http reranker request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("http reranker failed with status %d: %s", resp.StatusCode, string(body))
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read http reranker response: %w", err)
		}

		var outputIDs []int
		if err := json.Unmarshal(bodyBytes, &outputIDs); err != nil {
			var outputObjects []struct {
				ID int `json:"id"`
			}
			if err2 := json.Unmarshal(bodyBytes, &outputObjects); err2 != nil {
				return nil, fmt.Errorf("failed to parse http reranker output: %w (original error: %w)", err2, err)
			}
			for _, obj := range outputObjects {
				outputIDs = append(outputIDs, obj.ID)
			}
		}

		idToSnippet := make(map[int]Snippet, len(snippets))
		for _, s := range snippets {
			idToSnippet[s.ID] = s
		}

		var reordered []Snippet
		seen := make(map[int]bool, len(snippets))
		for _, id := range outputIDs {
			if s, ok := idToSnippet[id]; ok && !seen[id] {
				reordered = append(reordered, s)
				seen[id] = true
			}
		}

		for _, s := range snippets {
			if !seen[s.ID] {
				reordered = append(reordered, s)
			}
		}

		return reordered, nil
	}

	inputStruct := struct {
		Query      string    `json:"query"`
		Candidates []Snippet `json:"candidates"`
	}{
		Query:      query,
		Candidates: snippets,
	}
	inputBytes, err := json.Marshal(inputStruct)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal reranker input: %w", err)
	}

	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return snippets, nil
	}
	cmdName := parts[0]
	cmdArgs := parts[1:]

	cmd := exec.Command(cmdName, cmdArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewReader(inputBytes)

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("external reranker failed: %w (stderr: %s)", err, stderr.String())
	}

	var outputIDs []int
	if err := json.Unmarshal(stdout.Bytes(), &outputIDs); err != nil {
		var outputObjects []struct {
			ID int `json:"id"`
		}
		if err2 := json.Unmarshal(stdout.Bytes(), &outputObjects); err2 != nil {
			return nil, fmt.Errorf("failed to parse reranker output: %w (original error: %w)", err2, err)
		}
		for _, obj := range outputObjects {
			outputIDs = append(outputIDs, obj.ID)
		}
	}

	idToSnippet := make(map[int]Snippet, len(snippets))
	for _, s := range snippets {
		idToSnippet[s.ID] = s
	}

	var reordered []Snippet
	seen := make(map[int]bool, len(snippets))
	for _, id := range outputIDs {
		if s, ok := idToSnippet[id]; ok && !seen[id] {
			reordered = append(reordered, s)
			seen[id] = true
		}
	}

	for _, s := range snippets {
		if !seen[s.ID] {
			reordered = append(reordered, s)
		}
	}

	return reordered, nil
}
