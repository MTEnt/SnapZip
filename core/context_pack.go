package core

import (
	"database/sql"
	"fmt"
	"strings"
)

const DefaultContextPackBudgetBytes = 12 * 1024
const MinContextPackBudgetBytes = 512
const MaxContextPackBudgetBytes = 200 * 1024
const maxContextPackFeedbackInputBytes = 512
const maxContextPackFeedbackOutputBytes = 768

type SearchResult struct {
	Query         string           `json:"query"`
	ExpandedQuery string           `json:"expanded_query,omitempty"`
	Mode          string           `json:"mode,omitempty"`
	Snippets      []Snippet        `json:"snippets"`
	Receipts      []ContextReceipt `json:"receipts,omitempty"`
	Feedback      []Feedback       `json:"feedback,omitempty"`
}

type ContextPack struct {
	Query         string           `json:"query"`
	ExpandedQuery string           `json:"expanded_query,omitempty"`
	Mode          string           `json:"mode,omitempty"`
	BudgetBytes   int              `json:"budget_bytes"`
	UsedBytes     int              `json:"used_bytes"`
	Truncated     bool             `json:"truncated"`
	Quality       ContextQuality   `json:"quality"`
	Snippets      []Snippet        `json:"snippets"`
	Receipts      []ContextReceipt `json:"receipts,omitempty"`
	Feedback      []Feedback       `json:"feedback,omitempty"`
}

type ContextReceipt struct {
	Rank       int      `json:"rank"`
	Path       string   `json:"path,omitempty"`
	StartLine  int      `json:"start_line,omitempty"`
	EndLine    int      `json:"end_line,omitempty"`
	Topic      string   `json:"topic,omitempty"`
	Language   string   `json:"language,omitempty"`
	Score      float64  `json:"score"`
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons"`
	Evidence   []string `json:"evidence,omitempty"`
}

func SearchMemory(db *sql.DB, comp Compressor, query string, limit int, feedbackLimit int) (SearchResult, error) {
	return SearchMemoryWithMode(db, comp, query, "", limit, feedbackLimit)
}

func SearchMemoryWithMode(db *sql.DB, comp Compressor, query, mode string, limit int, feedbackLimit int) (SearchResult, error) {
	limit = normalizeResultLimit(limit, 3)
	feedbackLimit = normalizeResultLimit(feedbackLimit, 0)
	mode = normalizePackMode(mode)
	expandedQuery := ExpandQueryForPackMode(query, mode)

	snippets, err := RetrieveSimilarSnippets(db, comp, expandedQuery, limit)
	if err != nil {
		return SearchResult{}, err
	}

	var feedback []Feedback
	if feedbackLimit > 0 {
		feedback, err = RetrieveNegativeFeedback(db, feedbackLimit)
		if err != nil {
			return SearchResult{}, err
		}
	}

	return SearchResult{
		Query:         query,
		ExpandedQuery: expandedQuery,
		Mode:          mode,
		Snippets:      snippets,
		Receipts:      genericReceiptsForSnippets(snippets, mode),
		Feedback:      feedback,
	}, nil
}

func BuildContextPack(db *sql.DB, comp Compressor, query string, limit int, budgetBytes int, feedbackLimit int) (ContextPack, error) {
	return BuildContextPackWithMode(db, comp, query, "", limit, budgetBytes, feedbackLimit)
}

func BuildContextPackWithMode(db *sql.DB, comp Compressor, query, mode string, limit int, budgetBytes int, feedbackLimit int) (ContextPack, error) {
	budgetBytes = normalizeContextPackBudget(budgetBytes)
	mode = normalizePackMode(mode)

	result, err := SearchMemoryWithMode(db, comp, query, mode, limit, feedbackLimit)
	if err != nil {
		return ContextPack{}, err
	}

	return buildContextPackFromResult(query, mode, budgetBytes, result), nil
}

func buildContextPackFromResult(query, mode string, budgetBytes int, result SearchResult) ContextPack {
	pack := ContextPack{
		Query:         query,
		ExpandedQuery: result.ExpandedQuery,
		Mode:          mode,
		BudgetBytes:   budgetBytes,
		Feedback:      compactFeedbackForContextPack(result.Feedback),
	}

	for len(pack.Feedback) > 0 && len([]byte(RenderContextPack(pack))) > budgetBytes {
		pack.Feedback = pack.Feedback[:len(pack.Feedback)-1]
		pack.Truncated = true
	}

	for _, snippet := range result.Snippets {
		next := pack
		next.Snippets = append(append([]Snippet{}, pack.Snippets...), snippet)
		if len([]byte(RenderContextPack(next))) <= budgetBytes {
			pack.Snippets = next.Snippets
			continue
		}

		truncated := snippet
		truncated.Content = ""
		overhead := pack
		overhead.Truncated = true
		overhead.Snippets = append(append([]Snippet{}, pack.Snippets...), truncated)
		remaining := budgetBytes - len([]byte(RenderContextPack(overhead))) - len("\n...[truncated]\n")
		if remaining > 80 {
			truncated.Content = truncateWithMarker(snippet.Content, remaining)
			pack.Snippets = append(pack.Snippets, truncated)
		}
		pack.Truncated = true
		break
	}

	if len(pack.Snippets) < len(result.Snippets) {
		pack.Truncated = true
	}
	pack.Receipts = alignReceiptsWithSnippets(pack.Snippets, result.Receipts)
	return fitContextPackToBudget(pack, budgetBytes)
}

func RenderSearchResult(result SearchResult) string {
	var builder strings.Builder

	if len(result.Feedback) > 0 {
		builder.WriteString("SnapZip feedback memory:\n")
		for _, feedback := range result.Feedback {
			if feedback.BotResponse != "" {
				fmt.Fprintf(&builder, "- Problem: %q | Failed output: %q\n", feedback.UserInput, feedback.BotResponse)
			} else {
				fmt.Fprintf(&builder, "- Problem: %q\n", feedback.UserInput)
			}
		}
		builder.WriteByte('\n')
	}

	fmt.Fprintf(&builder, "Found %d matching snippets:\n", len(result.Snippets))
	for _, snippet := range result.Snippets {
		fmt.Fprintf(
			&builder,
			"\n--- Topic: %s (Language: %s | Location: %s | Relevance Score: %.4f) ---\n%s\n",
			snippet.Topic,
			snippet.Language,
			snippetLocation(snippet),
			snippet.Score,
			snippet.Content,
		)
	}
	return builder.String()
}

func RenderContextPack(pack ContextPack) string {
	var builder strings.Builder
	builder.WriteString("# SnapZip Context Pack\n\n")
	fmt.Fprintf(&builder, "Query: %s\n", pack.Query)
	if pack.Mode != "" {
		fmt.Fprintf(&builder, "Mode: %s\n", pack.Mode)
	}
	if pack.ExpandedQuery != "" && pack.ExpandedQuery != pack.Query {
		fmt.Fprintf(&builder, "Expanded query: %s\n", pack.ExpandedQuery)
	}
	fmt.Fprintf(&builder, "Budget: %d bytes\n", pack.BudgetBytes)
	if pack.Truncated {
		builder.WriteString("Truncated: true\n")
	}

	if len(pack.Feedback) > 0 {
		builder.WriteString("\n## Feedback Memory\n\n")
		for _, feedback := range pack.Feedback {
			if feedback.BotResponse != "" {
				fmt.Fprintf(&builder, "- Problem: %q | Failed output: %q\n", feedback.UserInput, feedback.BotResponse)
			} else {
				fmt.Fprintf(&builder, "- Problem: %q\n", feedback.UserInput)
			}
		}
	}

	if shouldRenderContextQuality(pack.Quality) {
		renderContextQuality(&builder, pack.Quality)
	}

	builder.WriteString("\n## Retrieved Snippets\n")
	if len(pack.Snippets) == 0 {
		builder.WriteString("\nNo matching snippets found.\n")
		return builder.String()
	}

	if len(pack.Receipts) > 0 {
		builder.WriteString("\n## Context Receipts\n")
		for _, receipt := range pack.Receipts {
			fmt.Fprintf(&builder, "\n%d. %s", receipt.Rank, receiptLocation(receipt))
			if receipt.Confidence > 0 {
				fmt.Fprintf(&builder, " (confidence %.2f)", receipt.Confidence)
			}
			builder.WriteByte('\n')
			for _, reason := range receipt.Reasons {
				fmt.Fprintf(&builder, "   - %s\n", reason)
			}
			for _, evidence := range receipt.Evidence {
				fmt.Fprintf(&builder, "   - Evidence: %s\n", evidence)
			}
		}
	}

	for idx, snippet := range pack.Snippets {
		fmt.Fprintf(&builder, "\n### %d. %s\n\n", idx+1, snippet.Topic)
		fmt.Fprintf(&builder, "Language: %s | Location: %s | Relevance Score: %.4f\n\n", snippet.Language, snippetLocation(snippet), snippet.Score)
		fmt.Fprintf(&builder, "```%s\n%s\n```\n", codeFenceLanguage(snippet.Language), snippet.Content)
	}
	return builder.String()
}

func fitContextPackToBudget(pack ContextPack, budgetBytes int) ContextPack {
	for {
		pack.Quality = ScoreContextPack(pack)
		usedBytes := len([]byte(RenderContextPack(pack)))
		if usedBytes <= budgetBytes {
			for range 4 {
				pack.UsedBytes = usedBytes
				pack.Quality = ScoreContextPack(pack)
				nextBytes := len([]byte(RenderContextPack(pack)))
				if nextBytes == usedBytes {
					return pack
				}
				usedBytes = nextBytes
				if usedBytes > budgetBytes {
					break
				}
			}
			if usedBytes > budgetBytes {
				continue
			}
			pack.UsedBytes = usedBytes
			pack.Quality = ScoreContextPack(pack)
			return pack
		}

		pack.Truncated = true
		switch {
		case len(pack.Feedback) > 0:
			pack.Feedback = pack.Feedback[:len(pack.Feedback)-1]
		case shrinkLastContextSnippet(&pack, usedBytes-budgetBytes):
			pack.Receipts = alignReceiptsWithSnippets(pack.Snippets, pack.Receipts)
			continue
		case len(pack.Receipts) > 1:
			pack.Receipts = pack.Receipts[:len(pack.Receipts)-1]
		case len(pack.Receipts) > 0:
			pack.Receipts = nil
		default:
			pack.UsedBytes = usedBytes
			pack.Quality = ScoreContextPack(pack)
			return pack
		}
	}
}

func shrinkLastContextSnippet(pack *ContextPack, overage int) bool {
	if len(pack.Snippets) == 0 {
		return false
	}
	lastIdx := len(pack.Snippets) - 1
	content := pack.Snippets[lastIdx].Content
	if len(content) == 0 {
		if len(pack.Snippets) > 1 {
			pack.Snippets = pack.Snippets[:lastIdx]
			return true
		}
		return false
	}

	targetBytes := len(content) - overage - 80
	if targetBytes < 80 {
		if len(pack.Snippets) > 1 {
			pack.Snippets = pack.Snippets[:lastIdx]
			return true
		}
		targetBytes = 80
	}
	if targetBytes >= len(content) {
		targetBytes = len(content) - 1
	}
	if targetBytes <= 0 {
		return false
	}
	next := truncateWithMarker(content, targetBytes)
	if next == content {
		return false
	}
	pack.Snippets[lastIdx].Content = next
	pack.Snippets[lastIdx].ContentHash = contentHash([]byte(next))
	return true
}

func normalizeResultLimit(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value > 100 {
		return 100
	}
	return value
}

func normalizeContextPackBudget(value int) int {
	if value <= 0 {
		return DefaultContextPackBudgetBytes
	}
	if value < MinContextPackBudgetBytes {
		return MinContextPackBudgetBytes
	}
	if value > MaxContextPackBudgetBytes {
		return MaxContextPackBudgetBytes
	}
	return value
}

func ExpandQueryForPackMode(query, mode string) string {
	query = strings.TrimSpace(query)
	switch normalizePackMode(mode) {
	case "debug":
		return strings.TrimSpace(query + " error failure exception stack trace test regression")
	case "refactor":
		return strings.TrimSpace(query + " interface implementation usage caller callee dependency")
	case "test":
		return strings.TrimSpace(query + " test spec fixture assertion mock benchmark")
	case "docs":
		return strings.TrimSpace(query + " readme docs documentation install setup config workflow")
	default:
		return query
	}
}

func normalizePackMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "default", "general":
		return ""
	case "debug", "refactor", "test", "docs":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return ""
	}
}

func genericReceiptsForSnippets(snippets []Snippet, mode string) []ContextReceipt {
	receipts := make([]ContextReceipt, 0, len(snippets))
	for idx, snippet := range snippets {
		reasons := []string{"retrieved by hybrid FTS5/QND ranking"}
		if mode != "" {
			reasons = append(reasons, "query expanded for "+mode+" mode")
		}
		receipts = append(receipts, receiptForSnippet(idx+1, snippet, 0.55, reasons, nil))
	}
	return receipts
}

func alignReceiptsWithSnippets(snippets []Snippet, receipts []ContextReceipt) []ContextReceipt {
	if len(snippets) == 0 {
		return nil
	}
	byKey := map[string]ContextReceipt{}
	for _, receipt := range receipts {
		key := receipt.Path + ":" + fmt.Sprint(receipt.StartLine) + ":" + fmt.Sprint(receipt.EndLine)
		byKey[key] = receipt
	}

	aligned := make([]ContextReceipt, 0, len(snippets))
	for idx, snippet := range snippets {
		key := snippet.Path + ":" + fmt.Sprint(snippet.StartLine) + ":" + fmt.Sprint(snippet.EndLine)
		receipt, ok := byKey[key]
		if !ok {
			receipt = receiptForSnippet(idx+1, snippet, 0.45, []string{"included in final context pack"}, nil)
		}
		receipt.Rank = idx + 1
		aligned = append(aligned, receipt)
	}
	return aligned
}

func receiptForSnippet(rank int, snippet Snippet, confidence float64, reasons []string, evidence []string) ContextReceipt {
	return ContextReceipt{
		Rank:       rank,
		Path:       snippet.Path,
		StartLine:  snippet.StartLine,
		EndLine:    snippet.EndLine,
		Topic:      snippet.Topic,
		Language:   snippet.Language,
		Score:      snippet.Score,
		Confidence: confidence,
		Reasons:    uniqueStrings(reasons),
		Evidence:   uniqueStrings(evidence),
	}
}

func receiptLocation(receipt ContextReceipt) string {
	if receipt.Path == "" {
		if receipt.Topic != "" {
			return receipt.Topic
		}
		return "unknown"
	}
	if receipt.StartLine > 0 && receipt.EndLine > 0 {
		if receipt.StartLine == receipt.EndLine {
			return fmt.Sprintf("%s:%d", receipt.Path, receipt.StartLine)
		}
		return fmt.Sprintf("%s:%d-%d", receipt.Path, receipt.StartLine, receipt.EndLine)
	}
	return receipt.Path
}

func truncateText(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	if cut > len(value) {
		cut = len(value)
	}
	if newline := strings.LastIndexByte(value[:cut], '\n'); newline > cut/2 {
		cut = newline
	}
	return strings.TrimRight(value[:cut], "\n\r\t ")
}

func truncateWithMarker(value string, maxBytes int) string {
	const marker = "\n...[truncated]"
	if maxBytes <= len(marker) || len(value) <= maxBytes {
		return truncateText(value, maxBytes)
	}
	return truncateText(value, maxBytes-len(marker)) + marker
}

func compactFeedbackForContextPack(feedback []Feedback) []Feedback {
	if len(feedback) == 0 {
		return nil
	}

	compacted := make([]Feedback, 0, len(feedback))
	for _, item := range feedback {
		if len(item.UserInput) > maxContextPackFeedbackInputBytes {
			item.UserInput = truncateWithMarker(item.UserInput, maxContextPackFeedbackInputBytes)
		}
		if len(item.BotResponse) > maxContextPackFeedbackOutputBytes {
			item.BotResponse = truncateWithMarker(item.BotResponse, maxContextPackFeedbackOutputBytes)
		}
		compacted = append(compacted, item)
	}
	return compacted
}

func codeFenceLanguage(language string) string {
	language = NormalizeLanguage(language)
	if language == "" {
		return "text"
	}
	var builder strings.Builder
	for _, r := range language {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '#' || r == '+' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "text"
	}
	return builder.String()
}

func snippetLocation(snippet Snippet) string {
	if snippet.Path == "" {
		return "unknown"
	}
	if snippet.StartLine > 0 && snippet.EndLine > 0 {
		if snippet.StartLine == snippet.EndLine {
			return fmt.Sprintf("%s:%d", snippet.Path, snippet.StartLine)
		}
		return fmt.Sprintf("%s:%d-%d", snippet.Path, snippet.StartLine, snippet.EndLine)
	}
	return snippet.Path
}
