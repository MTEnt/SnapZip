package core

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

func BuildRepairContextPack(db *sql.DB, comp Compressor, failureOutput, extraQuery, mode string, limit int, budgetBytes int, feedbackLimit int) (ContextPack, error) {
	budgetBytes = normalizeContextPackBudget(budgetBytes)
	limit = normalizeResultLimit(limit, 6)
	mode = normalizePackMode(mode)

	analysis := AnalyzeFailureOutput(failureOutput, extraQuery)
	query := strings.TrimSpace(analysis.Query)
	if query == "" {
		query = strings.TrimSpace(extraQuery)
	}
	if query == "" {
		query = strings.TrimSpace(failureOutput)
	}

	searchLimit := limit * 3
	if searchLimit < 12 {
		searchLimit = 12
	}
	result, err := SearchMemoryWithMode(db, comp, query, mode, searchLimit, feedbackLimit)
	if err != nil {
		return ContextPack{}, err
	}
	focused, err := repairFocusedSnippets(db, analysis, limit)
	if err != nil {
		return ContextPack{}, err
	}
	result.Query = query
	result.Snippets = mergeRepairSnippets(focused, result.Snippets, limit)
	result.Receipts = repairReceiptsForSnippets(result.Snippets, analysis, result.Receipts)
	return buildContextPackFromResult(query, mode, budgetBytes, result), nil
}

func repairFocusedSnippets(db *sql.DB, analysis FailureAnalysis, limit int) ([]Snippet, error) {
	type scoredSnippet struct {
		snippet Snippet
		score   int
	}
	var scored []scoredSnippet
	filePathSet := failureRefPathSet(analysis.FileRefs)
	symbolTerms := uniqueFailureTerms(append(append([]string{}, analysis.Symbols...), analysis.Identifiers...))

	for _, symbol := range repairSymbolMatches(db, symbolTerms, limit*8) {
		snippet, ok, err := indexedSnippetAtLine(db, symbol.Path, symbol.Line, 32)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		snippet.Score = -2.0
		scored = append(scored, scoredSnippet{
			snippet: snippet,
			score:   repairSymbolRank(symbol, symbolTerms, filePathSet),
		})
	}

	for idx, ref := range analysis.FileRefs {
		radius := 28
		if ref.Function != "" {
			radius = 36
		}
		snippet, ok, err := indexedSnippetAtLine(db, ref.Path, ref.Line, radius)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		snippet.Score = -1.5
		score := 30 + min(idx, 20)
		if ref.Function != "" {
			score += 8
		}
		if !isTestPath(ref.Path) {
			score += 15
		} else {
			score -= 10
		}
		if isDependencyPath(ref.Path) {
			score -= 30
		}
		scored = append(scored, scoredSnippet{snippet: snippet, score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].snippet.Path < scored[j].snippet.Path
		}
		return scored[i].score > scored[j].score
	})

	result := make([]Snippet, 0, len(scored))
	seen := map[string]bool{}
	for _, item := range scored {
		key := snippetDedupeKey(item.snippet)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item.snippet)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func repairSymbolMatches(db *sql.DB, terms []string, limit int) []Symbol {
	seen := map[string]bool{}
	var symbols []Symbol
	for _, term := range terms {
		if commonFailureSymbol(term) {
			continue
		}
		matches, err := SearchSymbols(db, term, limit)
		if err != nil {
			continue
		}
		for _, symbol := range matches {
			key := symbol.Path + ":" + symbol.Name + ":" + fmt.Sprint(symbol.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			symbols = append(symbols, symbol)
		}
	}
	return symbols
}

func repairSymbolRank(symbol Symbol, terms []string, filePathSet map[string]bool) int {
	score := 0
	lowerName := strings.ToLower(symbol.Name)
	lowerPath := strings.ToLower(symbol.Path)
	for _, term := range terms {
		lowerTerm := strings.ToLower(term)
		switch {
		case lowerName == lowerTerm:
			score += 40
		case strings.Contains(lowerName, lowerTerm):
			score += 18
		case strings.Contains(lowerPath, lowerTerm):
			score += 8
		}
	}
	for path := range filePathSet {
		if pathSuffixMatch(symbol.Path, path) {
			score += 12
			break
		}
	}
	if isTestPath(symbol.Path) {
		score -= 25
	}
	if strings.HasPrefix(lowerName, "test") {
		score -= 15
	}
	return score
}

func indexedSnippetAtLine(db *sql.DB, path string, line int, radius int) (Snippet, bool, error) {
	for _, candidate := range failurePathCandidates(path) {
		snippet, ok, err := indexedSnippetForPathCandidate(db, candidate, line)
		if err != nil || !ok {
			if err != nil {
				return Snippet{}, false, err
			}
			continue
		}
		return focusSnippet(snippet, line, radius), true, nil
	}
	return Snippet{}, false, nil
}

func indexedSnippetForPathCandidate(db *sql.DB, path string, line int) (Snippet, bool, error) {
	if path == "" {
		return Snippet{}, false, nil
	}
	likePath := "%/" + path
	query := `
		SELECT id, language, topic, content, path, start_line, end_line, content_hash, source_mtime
		FROM knowledge
		WHERE (path = ? OR path LIKE ?)
	`
	args := []any{path, likePath}
	if line > 0 {
		query += " AND start_line <= ? AND end_line >= ?"
		args = append(args, line, line)
	}
	query += `
		ORDER BY
			CASE WHEN path = ? THEN 0 ELSE 1 END,
			CASE WHEN ? > 0 THEN ABS(start_line - ?) ELSE 0 END,
			(end_line - start_line) ASC,
			id ASC
		LIMIT 1`
	args = append(args, path, line, line)

	var snippet Snippet
	err := db.QueryRow(query, args...).Scan(
		&snippet.ID,
		&snippet.Language,
		&snippet.Topic,
		&snippet.Content,
		&snippet.Path,
		&snippet.StartLine,
		&snippet.EndLine,
		&snippet.ContentHash,
		&snippet.SourceMTime,
	)
	if err == sql.ErrNoRows && line > 0 {
		return indexedSnippetForPathCandidate(db, path, 0)
	}
	if err == sql.ErrNoRows {
		return Snippet{}, false, nil
	}
	if err != nil {
		return Snippet{}, false, err
	}
	return snippet, true, nil
}

func focusSnippet(snippet Snippet, centerLine int, radius int) Snippet {
	if radius <= 0 || centerLine <= 0 || snippet.StartLine <= 0 {
		return snippet
	}

	lines := strings.Split(snippet.Content, "\n")
	if len(lines) == 0 {
		return snippet
	}
	startLine := centerLine - radius
	if startLine < snippet.StartLine {
		startLine = snippet.StartLine
	}
	endLine := centerLine + radius
	if snippet.EndLine > 0 && endLine > snippet.EndLine {
		endLine = snippet.EndLine
	}
	startIdx := startLine - snippet.StartLine
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := endLine - snippet.StartLine
	if endIdx >= len(lines) {
		endIdx = len(lines) - 1
	}
	if startIdx > endIdx {
		return snippet
	}

	focused := snippet
	focused.StartLine = startLine
	focused.EndLine = startLine + (endIdx - startIdx)
	focused.Content = strings.Join(lines[startIdx:endIdx+1], "\n")
	focused.ContentHash = contentHash([]byte(focused.Content))
	if focused.Path != "" {
		focused.Topic = fmt.Sprintf("Source excerpt: %s:%d-%d", focused.Path, focused.StartLine, focused.EndLine)
	}
	return focused
}

func mergeRepairSnippets(focused []Snippet, fallback []Snippet, limit int) []Snippet {
	limit = normalizeResultLimit(limit, 6)
	merged := make([]Snippet, 0, limit)
	seen := map[string]bool{}
	for _, group := range [][]Snippet{focused, fallback} {
		for _, snippet := range group {
			key := snippetDedupeKey(snippet)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, snippet)
			if len(merged) >= limit {
				return merged
			}
		}
	}
	return merged
}

func repairReceiptsForSnippets(snippets []Snippet, analysis FailureAnalysis, fallback []ContextReceipt) []ContextReceipt {
	fallbackByKey := map[string]ContextReceipt{}
	for _, receipt := range fallback {
		key := receipt.Path + ":" + fmt.Sprint(receipt.StartLine) + ":" + fmt.Sprint(receipt.EndLine)
		fallbackByKey[key] = receipt
	}

	receipts := make([]ContextReceipt, 0, len(snippets))
	for idx, snippet := range snippets {
		reasons, evidence, confidence := repairReceiptDetails(snippet, analysis)
		if len(reasons) == 0 {
			key := snippet.Path + ":" + fmt.Sprint(snippet.StartLine) + ":" + fmt.Sprint(snippet.EndLine)
			if fallbackReceipt, ok := fallbackByKey[key]; ok {
				fallbackReceipt.Rank = idx + 1
				receipts = append(receipts, fallbackReceipt)
				continue
			}
			reasons = []string{"fallback retrieval match after repair-focused candidates"}
			confidence = 0.45
		}
		receipts = append(receipts, receiptForSnippet(idx+1, snippet, confidence, reasons, evidence))
	}
	return receipts
}

func repairReceiptDetails(snippet Snippet, analysis FailureAnalysis) ([]string, []string, float64) {
	var reasons []string
	var evidence []string
	confidence := 0.45

	if snippet.Score <= -1.99 {
		reasons = append(reasons, "matched an indexed symbol or identifier from the failure output")
		confidence = 0.78
	}
	if snippet.Score <= -1.49 && snippet.Score > -1.99 {
		reasons = append(reasons, "matched a concrete file/line reference from the failure output")
		confidence = 0.72
	}

	for _, ref := range analysis.FileRefs {
		if pathSuffixMatch(snippet.Path, ref.Path) {
			if ref.Line > 0 && lineWithinSnippet(ref.Line, snippet) {
				reasons = append(reasons, "covers failure stack location")
				evidence = append(evidence, fmt.Sprintf("%s:%d", ref.Path, ref.Line))
				confidence = maxFloat(confidence, 0.86)
			} else {
				reasons = append(reasons, "matches a file mentioned in the failure output")
				evidence = append(evidence, ref.Path)
				confidence = maxFloat(confidence, 0.68)
			}
			if ref.Function != "" {
				evidence = append(evidence, "frame function "+ref.Function)
			}
		}
	}

	for _, symbol := range analysis.Symbols {
		if symbol != "" && strings.Contains(snippet.Content, symbol) {
			reasons = append(reasons, "contains failure-related symbol "+symbol)
			evidence = append(evidence, symbol)
			confidence = maxFloat(confidence, 0.82)
		}
	}
	for _, identifier := range analysis.Identifiers {
		if identifier != "" && strings.Contains(snippet.Content, identifier) {
			reasons = append(reasons, "contains failure-related identifier "+identifier)
			evidence = append(evidence, identifier)
			confidence = maxFloat(confidence, 0.76)
		}
	}

	if isTestPath(snippet.Path) {
		reasons = append(reasons, "included as failing or related test context")
		confidence = minFloat(confidence, 0.70)
	} else if len(reasons) > 0 {
		reasons = append(reasons, "source file ranked ahead of test/dependency context")
		confidence = maxFloat(confidence, 0.80)
	}
	if isDependencyPath(snippet.Path) {
		reasons = append(reasons, "dependency path retained only as supporting stack context")
		confidence = minFloat(confidence, 0.50)
	}

	return uniqueStrings(reasons), uniqueStrings(evidence), confidence
}

func snippetDedupeKey(snippet Snippet) string {
	if snippet.Path != "" {
		return fmt.Sprintf("%s:%d:%d", snippet.Path, snippet.StartLine, snippet.EndLine)
	}
	if snippet.ContentHash != "" {
		return snippet.ContentHash
	}
	return snippet.Topic
}

func failureRefPathSet(refs []FailureFileRef) map[string]bool {
	result := map[string]bool{}
	for _, ref := range refs {
		for _, candidate := range failurePathCandidates(ref.Path) {
			result[candidate] = true
		}
	}
	return result
}

func failurePathCandidates(path string) []string {
	path = strings.Trim(strings.TrimSpace(path), "\"'`")
	path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	var candidates []string
	for start := 0; start < len(parts); start++ {
		candidate := strings.Join(parts[start:], "/")
		if candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	return uniqueFailureTerms(candidates)
}

func pathSuffixMatch(indexedPath, failurePath string) bool {
	indexedPath = strings.TrimPrefix(strings.ReplaceAll(indexedPath, "\\", "/"), "./")
	failurePath = strings.TrimPrefix(strings.ReplaceAll(failurePath, "\\", "/"), "./")
	return indexedPath == failurePath || strings.HasSuffix(indexedPath, "/"+failurePath) || strings.HasSuffix(failurePath, "/"+indexedPath)
}

func isTestPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.Contains(path, "/test/") ||
		strings.Contains(path, "/tests/") ||
		strings.Contains(path, "test_") ||
		strings.Contains(path, "_test.") ||
		strings.Contains(path, ".test.") ||
		strings.Contains(path, ".spec.")
}

func isDependencyPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.Contains(path, "/.venv/") ||
		strings.Contains(path, "/venv/") ||
		strings.Contains(path, "/site-packages/") ||
		strings.Contains(path, "/node_modules/") ||
		strings.Contains(path, "/vendor/")
}

func lineWithinSnippet(line int, snippet Snippet) bool {
	return line > 0 && snippet.StartLine > 0 && snippet.EndLine > 0 && line >= snippet.StartLine && line <= snippet.EndLine
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
