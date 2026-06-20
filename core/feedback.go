package core

import (
	"database/sql"
	"strings"
)

var negativeWords = []string{
	"bad",
	"broken",
	"crap",
	"dumb",
	"error",
	"fail",
	"garbage",
	"hate",
	"incorrect",
	"stupid",
	"trash",
	"useless",
	"wrong",
}

// Feedback represents a logged negative feedback entry.
type Feedback struct {
	ID          int    `json:"id"`
	Sentiment   string `json:"sentiment"`
	UserInput   string `json:"user_input"`
	BotResponse string `json:"bot_response"`
	CreatedAt   string `json:"created_at"`
}

// AddFeedback inserts a feedback record when negative sentiment is detected.
func AddFeedback(db *sql.DB, userInput, botResponse string) (bool, error) {
	lowerInput := strings.ToLower(userInput)
	detectedWord := ""
	for _, word := range negativeWords {
		if strings.Contains(lowerInput, word) {
			detectedWord = word
			break
		}
	}
	if detectedWord == "" {
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

// RetrieveNegativeFeedback returns recent negative feedback entries to guide the AI.
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
