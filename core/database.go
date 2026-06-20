package core

import (
	"database/sql"
	"path/filepath"
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

// InitDB sets up the main SQLite database and virtual FTS5 index tables
func InitDB(dir string) (*sql.DB, error) {
	DBPath = filepath.Join(dir, "memory.db")
	db, err := sql.Open("sqlite", DBPath)
	if err != nil {
		return nil, err
	}

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

	return db, nil
}

// AddKnowledge inserts a new codebase template or config note into SQLite and the FTS5 index
func AddKnowledge(db *sql.DB, language, topic, content string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO knowledge (language, topic, content) VALUES (?, ?, ?)",
		language, topic, content,
	)
	if err != nil {
		return err
	}

	rowID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	// Index in FTS5
	_, err = tx.Exec(
		"INSERT INTO knowledge_fts (rowid, topic, content) VALUES (?, ?, ?)",
		rowID, topic, content,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// DetectLanguage parses terms in a query to find the target programming language
func DetectLanguage(prompt string) string {
	promptLower := strings.ToLower(prompt)
	for _, lang := range []string{"python", "javascript", "bash", "sql", "go"} {
		if strings.Contains(promptLower, lang) {
			return lang
		}
		if lang == "javascript" && strings.Contains(promptLower, "js") {
			return "javascript"
		}
		if lang == "sql" && strings.Contains(promptLower, "sqlite") {
			return "sql"
		}
		if lang == "bash" && (strings.Contains(promptLower, "shell") || strings.Contains(promptLower, "sh")) {
			return "bash"
		}
	}
	return ""
}

// RetrieveSimilarSnippets executes FTS5 full-text lookup and then parallel QND compression re-ranking
func RetrieveSimilarSnippets(db *sql.DB, comp Compressor, prompt string, limit int) ([]Snippet, error) {
	detectedLang := DetectLanguage(prompt)
	
	// Tokenize prompt for FTS5 MATCH
	words := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(prompt, "'", " "), "\"", " "))
	var ftsTokens []string
	for _, w := range words {
		wLower := strings.ToLower(w)
		if detectedLang != "" && (wLower == detectedLang || wLower == "python" || wLower == "javascript" || wLower == "js" || wLower == "bash" || wLower == "sh" || wLower == "sql" || wLower == "go") {
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
		if detectedLang != "" {
			rows, err = db.Query(`
				SELECT k.id, k.language, k.topic, k.content 
				FROM knowledge k
				JOIN knowledge_fts f ON k.id = f.rowid
				WHERE knowledge_fts MATCH ? AND k.language = ?
				ORDER BY f.rank
				LIMIT 100`, ftsQuery, detectedLang)
		} else {
			rows, err = db.Query(`
				SELECT k.id, k.language, k.topic, k.content 
				FROM knowledge k
				JOIN knowledge_fts f ON k.id = f.rowid
				WHERE knowledge_fts MATCH ? 
				ORDER BY f.rank
				LIMIT 100`, ftsQuery)
		}
	}

	if err != nil || rows == nil {
		if detectedLang != "" {
			rows, err = db.Query("SELECT id, language, topic, content FROM knowledge WHERE language = ? ORDER BY id DESC LIMIT 100", detectedLang)
		} else {
			rows, err = db.Query("SELECT id, language, topic, content FROM knowledge ORDER BY id DESC LIMIT 100")
		}
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
		numWorkers := 18
		jobs := make(chan int, len(candidates))
		var wg sync.WaitGroup

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					candidates[idx].Score = CalculateQND(comp, prompt, candidates[idx].Content)
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

// Feedback struct represents a logged negative feedback entry
type Feedback struct {
	ID          int    `json:"id"`
	Sentiment   string `json:"sentiment"`
	UserInput   string `json:"user_input"`
	BotResponse string `json:"bot_response"`
	CreatedAt   string `json:"created_at"`
}

// AddFeedback inserts a new feedback record if negative sentiment is detected
func AddFeedback(db *sql.DB, userInput, botResponse string) (bool, error) {
	lowerInput := strings.ToLower(userInput)
	negativeWords := []string{
		"fuck", "shit", "crap", "garbage", "trash", "useless", "broken",
		"wrong", "incorrect", "fail", "error", "bad", "stupid", "dumb", "hate",
	}

	isNegative := false
	detectedWord := ""
	for _, word := range negativeWords {
		if strings.Contains(lowerInput, word) {
			isNegative = true
			detectedWord = word
			break
		}
	}

	if !isNegative {
		return false, nil
	}

	_, err := db.Exec(
		"INSERT INTO negative_feedback (sentiment, user_input, bot_response) VALUES (?, ?, ?)",
		detectedWord, userInput, botResponse,
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

// RetrieveNegativeFeedback returns recent negative feedback entries to guide the AI
func RetrieveNegativeFeedback(db *sql.DB, limit int) ([]Feedback, error) {
	rows, err := db.Query("SELECT id, sentiment, user_input, bot_response, created_at FROM negative_feedback ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.Sentiment, &f.UserInput, &f.BotResponse, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, nil
}
