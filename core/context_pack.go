package core

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const DefaultContextPackBudgetBytes = 12 * 1024
const MinContextPackBudgetBytes = 512
const MaxContextPackBudgetBytes = 200 * 1024
const maxContextPackFeedbackInputBytes = 512
const maxContextPackFeedbackOutputBytes = 768
const maxSymbolGraphNamesPerSeed = 8
const maxSymbolGraphRefsPerName = 8
const maxSymbolGraphCandidatesPerSeed = 16

type SearchResult struct {
	Query         string           `json:"query"`
	ExpandedQuery string           `json:"expanded_query,omitempty"`
	Mode          string           `json:"mode,omitempty"`
	Snippets      []Snippet        `json:"snippets"`
	Receipts      []ContextReceipt `json:"receipts,omitempty"`
	Feedback      []Feedback       `json:"feedback,omitempty"`
}

type SearchOptions struct {
	IncludeDiagnostics bool
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
	return SearchMemoryWithOptions(db, comp, query, mode, limit, feedbackLimit, SearchOptions{})
}

func SearchMemoryWithOptions(db *sql.DB, comp Compressor, query, mode string, limit int, feedbackLimit int, options SearchOptions) (SearchResult, error) {
	return searchMemoryWithMode(db, comp, query, mode, limit, feedbackLimit, true, options)
}

func searchMemoryWithMode(db *sql.DB, comp Compressor, query, mode string, limit int, feedbackLimit int, includeGraph bool, options SearchOptions) (SearchResult, error) {
	limit = normalizeResultLimit(limit, 3)
	feedbackLimit = normalizeResultLimit(feedbackLimit, 0)
	mode = normalizePackMode(mode)
	expandedQuery := ExpandQueryForPackMode(query, mode)

	snippets, err := RetrieveSimilarSnippetsWithOptions(db, comp, expandedQuery, limit, RetrieveOptions{IncludeDiagnostics: options.IncludeDiagnostics})
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

	result := SearchResult{
		Query:         query,
		ExpandedQuery: expandedQuery,
		Mode:          mode,
		Snippets:      snippets,
		Receipts:      genericReceiptsForSnippets(db, snippets, expandedQuery, mode),
		Feedback:      feedback,
	}
	if includeGraph {
		result = addGraphContextToSearchResult(db, result, limit, false)
	}
	return result, nil
}

func BuildContextPack(db *sql.DB, comp Compressor, query string, limit int, budgetBytes int, feedbackLimit int) (ContextPack, error) {
	return BuildContextPackWithMode(db, comp, query, "", limit, budgetBytes, feedbackLimit)
}

func BuildContextPackWithMode(db *sql.DB, comp Compressor, query, mode string, limit int, budgetBytes int, feedbackLimit int) (ContextPack, error) {
	budgetBytes = normalizeContextPackBudget(budgetBytes)
	mode = normalizePackMode(mode)
	finalLimit := normalizeResultLimit(limit, 5)
	searchLimit := finalLimit
	if finalLimit > 1 {
		searchLimit = min(finalLimit*2, 100)
	}

	result, err := searchMemoryWithMode(db, comp, query, mode, searchLimit, feedbackLimit, false, SearchOptions{})
	if err != nil {
		return ContextPack{}, err
	}
	result = addGraphContextToSearchResult(db, result, finalLimit, true)
	result = selectContextPackSnippets(result, finalLimit)

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
	if len(result.Snippets) > 0 && len(result.Receipts) > 0 {
		renderContextReceipts(&builder, result.Receipts)
	}
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
		renderContextReceipts(&builder, pack.Receipts)
	}

	for idx, snippet := range pack.Snippets {
		fmt.Fprintf(&builder, "\n### %d. %s\n\n", idx+1, snippet.Topic)
		fmt.Fprintf(&builder, "Language: %s | Location: %s | Relevance Score: %.4f\n\n", snippet.Language, snippetLocation(snippet), snippet.Score)
		fmt.Fprintf(&builder, "```%s\n%s\n```\n", codeFenceLanguage(snippet.Language), snippet.Content)
	}
	return builder.String()
}

func renderContextReceipts(builder *strings.Builder, receipts []ContextReceipt) {
	builder.WriteString("\n## Context Receipts\n")
	for _, receipt := range receipts {
		fmt.Fprintf(builder, "\n%d. %s", receipt.Rank, receiptLocation(receipt))
		if receipt.Confidence > 0 {
			fmt.Fprintf(builder, " (confidence %.2f)", receipt.Confidence)
		}
		builder.WriteByte('\n')
		for _, reason := range receipt.Reasons {
			fmt.Fprintf(builder, "   - %s\n", reason)
		}
		for _, evidence := range receipt.Evidence {
			fmt.Fprintf(builder, "   - Evidence: %s\n", evidence)
		}
	}
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
	mode = normalizePackMode(mode)
	modeTerms := ""
	switch normalizePackMode(mode) {
	case "debug":
		modeTerms = "error failure exception stack trace test regression"
	case "refactor":
		modeTerms = "interface implementation usage caller callee dependency"
	case "test":
		modeTerms = "test spec fixture assertion mock benchmark"
	case "docs":
		modeTerms = "readme docs documentation install setup config workflow"
	case "review":
		modeTerms = "code review diff change regression risk caller dependency test validation"
	default:
		return query
	}
	return strings.TrimSpace(strings.Join(expandedModeQueryParts(query, mode, modeTerms), " "))
}

func expandedModeQueryParts(query, mode, modeTerms string) []string {
	parts := []string{query}
	if mode != "" && looksLikeCodeContext(query) {
		planned := strings.TrimSpace(planRetrievalQuery(query).StructuredPrompt)
		if planned != "" && planned != query && !strings.Contains(query, planned) {
			parts = append(parts, planned)
		}
	}
	parts = append(parts, modeTerms)
	return parts
}

func normalizePackMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "default", "general":
		return ""
	case "debug", "refactor", "test", "docs", "review":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return ""
	}
}

func selectContextPackSnippets(result SearchResult, limit int) SearchResult {
	limit = normalizeResultLimit(limit, 5)
	if len(result.Snippets) <= 1 {
		return result
	}
	if len(result.Snippets) > limit {
		result.Snippets = result.Snippets[:limit]
		result.Receipts = alignReceiptsWithSnippets(result.Snippets, result.Receipts)
	}

	topScore := result.Snippets[0].Score
	margin := contextPackScoreMargin(result.Mode)
	queryTokens := uniqueStrings(searchTokens(firstNonEmpty(result.ExpandedQuery, result.Query)))
	tokenWeights := queryTokenWeights(queryTokens, result.Snippets)
	receiptsByKey := receiptsBySnippetKey(result.Receipts)

	selected := []Snippet{result.Snippets[0]}
	selectedReceipts := []ContextReceipt{receiptsByKey[snippetReceiptKey(result.Snippets[0])]}
	for _, snippet := range result.Snippets[1:] {
		receipt := receiptsByKey[snippetReceiptKey(snippet)]
		if shouldKeepContextPackSnippet(snippet, receipt, selected, selectedReceipts, topScore, margin, queryTokens, tokenWeights) {
			selected = append(selected, snippet)
			selectedReceipts = append(selectedReceipts, receipt)
		}
	}
	result.Snippets = selected
	result.Receipts = alignReceiptsWithSnippets(result.Snippets, result.Receipts)
	return result
}

func shouldKeepContextPackSnippet(snippet Snippet, receipt ContextReceipt, selected []Snippet, selectedReceipts []ContextReceipt, topScore, margin float64, queryTokens []string, tokenWeights map[string]float64) bool {
	if receiptHasRetrievalEvidence(receipt) {
		return true
	}
	if snippet.Score <= topScore+0.08 {
		return true
	}

	boost := lexicalBoost(queryTokens, tokenWeights, snippet)
	if boost <= 0 || snippet.Score > topScore+margin {
		return false
	}
	if selectedHasStrongContext(selected, selectedReceipts) && weakLexicalTailSnippet(snippet, queryTokens) {
		return false
	}
	return true
}

func contextPackScoreMargin(mode string) float64 {
	switch normalizePackMode(mode) {
	case "debug", "refactor", "test", "review":
		return 0.60
	default:
		return 0.40
	}
}

func receiptHasRetrievalEvidence(receipt ContextReceipt) bool {
	if len(receipt.Evidence) > 0 || receipt.Confidence >= 0.65 {
		return true
	}
	for _, reason := range receipt.Reasons {
		lower := strings.ToLower(reason)
		if strings.Contains(lower, "resolved local import graph") ||
			strings.Contains(lower, "failure-related") ||
			strings.Contains(lower, "source file ranked ahead") {
			return true
		}
	}
	return false
}

func selectedHasStrongContext(snippets []Snippet, receipts []ContextReceipt) bool {
	if len(snippets) < 2 {
		return false
	}

	evidenceCount := 0
	hasSource := false
	hasTest := false
	for idx, snippet := range snippets {
		if isTestPath(snippet.Path) {
			hasTest = true
		} else if !isDependencyPath(snippet.Path) {
			hasSource = true
		}
		if idx < len(receipts) && receiptHasRetrievalEvidence(receipts[idx]) {
			evidenceCount++
		}
	}
	return evidenceCount >= 2 || (evidenceCount > 0 && hasSource && hasTest)
}

func weakLexicalTailSnippet(snippet Snippet, queryTokens []string) bool {
	if !supportsSymbolReferences(snippet.Language) {
		return false
	}
	if isTestPath(snippet.Path) || isDependencyPath(snippet.Path) {
		return false
	}
	lowerTopic := strings.ToLower(snippet.Topic)
	if isDocTopic(lowerTopic) && queryWantsDocs(queryTokens) {
		return false
	}
	if isWorkflowTopic(lowerTopic) && queryWantsWorkflows(queryTokens) {
		return false
	}
	if len(ExtractSymbols(snippet.Language, snippet.Path, snippet.Content)) > 0 {
		return false
	}
	if len(ExtractSymbolReferences(snippet.Language, snippet.Path, snippet.Content)) > 0 {
		return false
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func genericReceiptsForSnippets(db *sql.DB, snippets []Snippet, query string, mode string) []ContextReceipt {
	receipts := make([]ContextReceipt, 0, len(snippets))
	for idx, snippet := range snippets {
		reasons := []string{"retrieved by hybrid FTS5/QND ranking"}
		if mode != "" {
			reasons = append(reasons, "query expanded for "+mode+" mode")
		}
		evidence := []string(nil)
		confidence := 0.55
		retrievalReasons, retrievalEvidence, retrievalConfidence := retrievalReceiptDetails(query, snippet)
		if len(retrievalReasons) > 0 {
			reasons = append(reasons, retrievalReasons...)
			evidence = append(evidence, retrievalEvidence...)
			confidence = maxFloat(confidence, retrievalConfidence)
		}
		importReasons, importEvidence := importReceiptDetails(db, snippet.Path)
		if len(importReasons) > 0 {
			reasons = append(reasons, importReasons...)
			evidence = append(evidence, importEvidence...)
			confidence = maxFloat(confidence, 0.62)
		}
		receipts = append(receipts, receiptForSnippet(idx+1, snippet, confidence, reasons, evidence))
	}
	return receipts
}

func retrievalReceiptDetails(query string, snippet Snippet) ([]string, []string, float64) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil, 0
	}
	plan := planRetrievalQuery(query)
	if len(plan.FTSPaths) <= 1 {
		return nil, nil, 0
	}

	snippetTokens := stringSet(searchTokens(snippet.Topic + " " + snippet.Path + " " + snippet.Content)...)
	var reasons []string
	var evidence []string
	confidence := 0.55
	for idx, path := range plan.FTSPaths {
		if idx == 0 || len(path) == 0 {
			continue
		}
		matched := matchedRetrievalPathTokens(path, snippetTokens)
		if len(matched) < minRetrievalPathMatches(path) {
			continue
		}
		label := retrievalPathReceiptLabel(query, idx, path, plan)
		if label == "expanded identifier retrieval path" {
			matched = nonPrimaryRetrievalTokens(matched, plan.FTSTokens)
			if len(matched) < 2 {
				continue
			}
		}
		reasons = append(reasons, "matched "+label)
		evidence = append(evidence, label+" terms: "+strings.Join(limitSearchTokens(matched, 8), ", "))
		confidence = maxFloat(confidence, retrievalPathConfidence(label))
	}
	return uniqueStrings(reasons), uniqueStrings(evidence), confidence
}

func matchedRetrievalPathTokens(path []string, snippetTokens map[string]bool) []string {
	var matched []string
	for _, token := range path {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" || !meaningfulCodeSearchToken(token) {
			continue
		}
		if snippetTokens[token] {
			matched = append(matched, token)
		}
	}
	return uniqueStrings(matched)
}

func nonPrimaryRetrievalTokens(tokens []string, primary []string) []string {
	primarySet := stringSet(primary...)
	var filtered []string
	for _, token := range tokens {
		if primarySet[token] {
			continue
		}
		filtered = append(filtered, token)
	}
	return uniqueStrings(filtered)
}

func minRetrievalPathMatches(path []string) int {
	meaningful := 0
	for _, token := range path {
		if meaningfulCodeSearchToken(token) {
			meaningful++
		}
	}
	if meaningful <= 1 {
		return meaningful
	}
	return 2
}

func retrievalPathReceiptLabel(query string, idx int, path []string, plan retrievalQueryPlan) string {
	if looksLikeCodeContext(query) && idx == 1 {
		return "compact code-context retrieval path"
	}
	if retrievalPathHasExpandedIdentifierTokens(path, plan.FTSTokens) {
		return "expanded identifier retrieval path"
	}
	return "expanded query retrieval path"
}

func retrievalPathHasExpandedIdentifierTokens(path []string, primary []string) bool {
	primarySet := stringSet(primary...)
	for _, token := range path {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" || primarySet[token] || !meaningfulCodeSearchToken(token) {
			continue
		}
		return true
	}
	return false
}

func retrievalPathConfidence(label string) float64 {
	switch label {
	case "expanded identifier retrieval path", "compact code-context retrieval path":
		return 0.62
	default:
		return 0.58
	}
}

type graphContextCandidate struct {
	snippet  Snippet
	receipt  ContextReceipt
	priority int
}

func addGraphContextToSearchResult(db *sql.DB, result SearchResult, limit int, includeDependentTypes bool) SearchResult {
	limit = normalizeResultLimit(limit, 5)
	if db == nil || limit <= 0 || len(result.Snippets) == 0 {
		return result
	}
	if len(result.Snippets) == 1 && limit <= 1 {
		return result
	}

	graphBudget := graphContextBudget(result.Mode, limit)
	candidates := graphContextCandidates(db, result.Snippets, graphBudget, result.Mode, includeDependentTypes)

	receiptByKey := map[string]ContextReceipt{}
	directSnippetByKey := map[string]Snippet{}
	directSnippetByID := map[int]Snippet{}
	for _, receipt := range result.Receipts {
		key := receipt.Path + ":" + fmt.Sprint(receipt.StartLine) + ":" + fmt.Sprint(receipt.EndLine)
		receiptByKey[key] = receipt
	}
	for _, snippet := range result.Snippets {
		directSnippetByKey[snippetDedupeKey(snippet)] = snippet
		if snippet.ID != 0 {
			directSnippetByID[snippet.ID] = snippet
		}
	}

	var merged []Snippet
	var receipts []ContextReceipt
	seen := map[string]bool{}
	mergedIndexByKey := map[string]int{}
	mergedIndexByID := map[int]int{}
	mergedIndexByPath := map[string]int{}
	mergeReceipt := func(idx int, receipt ContextReceipt) {
		if idx < 0 || idx >= len(receipts) || receipt.Path == "" {
			return
		}
		current := &receipts[idx]
		if current.Path == "" {
			receipt.Rank = idx + 1
			*current = receipt
			return
		}
		current.Reasons = uniqueStrings(append(current.Reasons, receipt.Reasons...))
		current.Evidence = uniqueStrings(append(current.Evidence, receipt.Evidence...))
		current.Confidence = maxFloat(current.Confidence, receipt.Confidence)
	}
	addSnippet := func(snippet Snippet, receipt ContextReceipt, dedupePath bool) bool {
		if len(merged) >= limit {
			return false
		}
		path := normalizeIndexedPath(snippet.Path)
		if dedupePath && path != "" {
			if existing, ok := mergedIndexByPath[path]; ok {
				mergeReceipt(existing, receipt)
				return false
			}
		}
		if snippet.ID != 0 {
			if existing, ok := mergedIndexByID[snippet.ID]; ok {
				mergeReceipt(existing, receipt)
				return false
			}
			if directSnippet, ok := directSnippetByID[snippet.ID]; ok {
				snippet = directSnippet
			}
		}
		key := snippetDedupeKey(snippet)
		if existing, ok := mergedIndexByKey[key]; ok {
			mergeReceipt(existing, receipt)
			return false
		}
		if directSnippet, ok := directSnippetByKey[key]; ok {
			snippet = directSnippet
		}
		seen[key] = true
		merged = append(merged, snippet)
		if path != "" {
			mergedIndexByPath[path] = len(merged) - 1
		}
		receiptKey := snippet.Path + ":" + fmt.Sprint(snippet.StartLine) + ":" + fmt.Sprint(snippet.EndLine)
		if directReceipt, ok := receiptByKey[receiptKey]; ok {
			if receipt.Path == "" {
				receipt = directReceipt
			} else {
				receipt.Reasons = uniqueStrings(append(directReceipt.Reasons, receipt.Reasons...))
				receipt.Evidence = uniqueStrings(append(directReceipt.Evidence, receipt.Evidence...))
				receipt.Confidence = maxFloat(directReceipt.Confidence, receipt.Confidence)
				receipt.Score = directReceipt.Score
			}
		}
		if receipt.Path == "" {
			receipt = receiptForSnippet(len(merged), snippet, 0.45, []string{"included in final context pack"}, nil)
		}
		receipt.Rank = len(merged)
		receipts = append(receipts, receipt)
		mergedIndexByKey[key] = len(merged) - 1
		if snippet.ID != 0 {
			mergedIndexByID[snippet.ID] = len(merged) - 1
		}
		return true
	}

	addSnippet(result.Snippets[0], ContextReceipt{}, false)
	graphAdded := 0
	for _, candidate := range candidates {
		if graphAdded >= graphBudget {
			break
		}
		if addSnippet(candidate.snippet, candidate.receipt, true) {
			graphAdded++
		}
	}
	for _, snippet := range result.Snippets[1:] {
		if len(merged) >= limit {
			break
		}
		addSnippet(snippet, ContextReceipt{}, false)
	}
	result.Snippets = merged
	result.Receipts = receipts
	return result
}

func graphContextBudget(mode string, limit int) int {
	if limit <= 1 {
		return 0
	}
	switch normalizePackMode(mode) {
	case "debug", "refactor", "test", "review":
		return min(2, limit-1)
	case "docs":
		return 0
	default:
		return 1
	}
}

func graphContextCandidates(db *sql.DB, seeds []Snippet, budget int, mode string, includeDependentTypes bool) []graphContextCandidate {
	if budget <= 0 {
		return nil
	}
	var candidates []graphContextCandidate
	seedLimit := min(len(seeds), 3)
	for _, seed := range seeds[:seedLimit] {
		if seed.Path == "" {
			continue
		}
		if includeDependentTypes {
			candidates = append(candidates, dependentTypeContextCandidates(db, seed)...)
		}
		candidates = append(candidates, outgoingGraphContextCandidates(db, seed)...)
		candidates = append(candidates, incomingGraphContextCandidates(db, seed)...)
		if includeSymbolReferenceGraph(mode) {
			candidates = append(candidates, symbolGraphContextCandidates(db, seed)...)
		}
	}
	sortGraphContextCandidates(candidates)
	return dedupeGraphContextCandidates(candidates)
}

func includeSymbolReferenceGraph(mode string) bool {
	switch normalizePackMode(mode) {
	case "debug", "refactor", "test", "review":
		return true
	default:
		return false
	}
}

func outgoingGraphContextCandidates(db *sql.DB, seed Snippet) []graphContextCandidate {
	var candidates []graphContextCandidate
	seenPaths := map[string]bool{seed.Path: true}

	resolveImports := func(currentPath string) []ImportReference {
		refs, err := importsForPath(db, currentPath)
		if err != nil {
			return nil
		}
		return refs
	}

	type queueItem struct {
		path  string
		depth int
	}
	queue := []queueItem{{path: seed.Path, depth: 0}}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if item.depth > 1 {
			continue
		}

		refs := resolveImports(item.path)
		for _, ref := range refs {
			if ref.TargetPath == "" || seenPaths[ref.TargetPath] {
				continue
			}
			seenPaths[ref.TargetPath] = true

			snippet, matchedSymbol, ok, err := importedTargetSnippet(db, ref)
			if err != nil || !ok {
				continue
			}

			scoreBoost := -0.65
			confidence := 0.68
			evidencePrefix := ""
			reasons := []string{
				"included through resolved local import graph",
				"targeted by a retrieved snippet's local import",
			}
			if item.depth > 0 {
				scoreBoost = -0.55
				confidence = 0.55
				reasons = append(reasons, "included as a transitive (depth 1) dependency")
				evidencePrefix = fmt.Sprintf("[transitive from %s] ", item.path)
			}

			snippet.Score = minFloat(snippet.Score, scoreBoost)
			evidence := fmt.Sprintf("%s%s imports %s -> %s", evidencePrefix, item.path, ref.ImportPath, ref.TargetPath)
			evidenceItems := []string{evidence}
			if matchedSymbol != "" {
				reasons = append(reasons, "focused on imported symbol definition")
				evidenceItems = append(evidenceItems, "imported symbol "+matchedSymbol)
			}

			receipt := receiptForSnippet(0, snippet, confidence, reasons, evidenceItems)
			candidates = append(candidates, graphContextCandidate{
				snippet:  snippet,
				receipt:  receipt,
				priority: graphContextPriority(snippet, "outgoing"),
			})

			if item.depth < 1 {
				queue = append(queue, queueItem{path: ref.TargetPath, depth: item.depth + 1})
			}
		}
	}
	return candidates
}

func importedTargetSnippet(db *sql.DB, ref ImportReference) (Snippet, string, bool, error) {
	for _, name := range importedSymbolNames(ref) {
		symbols, err := symbolsForPathAndName(db, ref.TargetPath, name)
		if err != nil {
			return Snippet{}, "", false, err
		}
		for _, symbol := range symbols {
			snippet, ok, err := indexedSnippetAtLine(db, symbol.Path, symbol.Line, 18)
			if err != nil || !ok {
				return Snippet{}, "", false, err
			}
			return snippet, symbol.Name, true, nil
		}
	}
	snippet, ok, err := indexedSnippetAtLine(db, ref.TargetPath, 0, 0)
	return snippet, "", ok, err
}

func importedSymbolNames(ref ImportReference) []string {
	var names []string
	add := func(value string) {
		for _, token := range identifierFields(value) {
			if token != "" && !protectedIdentifier(token) {
				names = append(names, token)
			}
		}
	}
	add(ref.Alias)

	context := stripLineComment(ref.Language, ref.Context)
	switch NormalizeLanguage(ref.Language) {
	case "py":
		if match := pythonFromPattern.FindStringSubmatch(context); len(match) > 2 {
			for _, part := range strings.Split(match[2], ",") {
				name, alias := splitAlias(part, " as ")
				add(name)
				add(alias)
			}
		}
	case "js", "jsx", "ts", "tsx", "mjs", "cjs":
		addJSImportNames(&names, context)
	}
	if len(names) == 0 && ref.Alias != "" {
		add(ref.Alias)
	}
	return uniqueStrings(names)
}

func addJSImportNames(names *[]string, context string) {
	add := func(value string) {
		for _, token := range identifierFields(value) {
			if token != "" && !protectedIdentifier(token) {
				*names = append(*names, token)
			}
		}
	}
	if start := strings.Index(context, "{"); start >= 0 {
		if end := strings.Index(context[start+1:], "}"); end >= 0 {
			for _, part := range strings.Split(context[start+1:start+1+end], ",") {
				name, alias := splitAlias(part, " as ")
				add(name)
				add(alias)
			}
		}
	}
	if match := jsImportPattern.FindStringSubmatch(strings.TrimSpace(context)); len(match) > 2 {
		prefix := strings.TrimSpace(match[1])
		if idx := strings.Index(prefix, "{"); idx >= 0 {
			prefix = strings.TrimSpace(prefix[:idx])
		}
		prefix = strings.Trim(prefix, ",")
		add(prefix)
	}
}

func symbolsForPathAndName(db *sql.DB, path, name string) ([]Symbol, error) {
	path = normalizeIndexedPath(path)
	name = strings.TrimSpace(name)
	if path == "" || name == "" {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT id, name, kind, signature, language, path, line, docstring
		FROM symbols
		WHERE path = ? AND lower(name) = lower(?)
		ORDER BY
			CASE kind
				WHEN 'class' THEN 0
				WHEN 'type' THEN 0
				WHEN 'function' THEN 1
				WHEN 'method' THEN 2
				ELSE 3
			END,
			line ASC
		LIMIT 5`, path, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []Symbol
	for rows.Next() {
		symbol, err := scanSymbol(rows)
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, symbol)
	}
	return symbols, rows.Err()
}

func incomingGraphContextCandidates(db *sql.DB, seed Snippet) []graphContextCandidate {
	var candidates []graphContextCandidate
	seenPaths := map[string]bool{seed.Path: true}

	type queueItem struct {
		path  string
		depth int
	}
	queue := []queueItem{{path: seed.Path, depth: 0}}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if item.depth > 1 {
			continue
		}

		refs, err := importsTargetingPath(db, item.path)
		if err != nil {
			continue
		}

		for _, ref := range refs {
			if ref.Path == "" || seenPaths[ref.Path] {
				continue
			}
			seenPaths[ref.Path] = true

			snippet, ok, err := indexedSnippetAtLine(db, ref.Path, ref.Line, 32)
			if err != nil || !ok {
				continue
			}

			scoreBoost := -0.70
			confidence := 0.72
			evidencePrefix := ""
			reasons := []string{
				"included through resolved local import graph",
				"imports a retrieved snippet",
			}
			if item.depth > 0 {
				scoreBoost = -0.58
				confidence = 0.58
				reasons = append(reasons, "included as a transitive (depth 1) incoming dependency")
				evidencePrefix = fmt.Sprintf("[transitive targeting %s] ", item.path)
			}

			snippet.Score = minFloat(snippet.Score, scoreBoost)
			evidence := fmt.Sprintf("%s%s imports %s -> %s", evidencePrefix, ref.Path, ref.ImportPath, item.path)
			receipt := receiptForSnippet(0, snippet, confidence, reasons, []string{evidence})
			candidates = append(candidates, graphContextCandidate{
				snippet:  snippet,
				receipt:  receipt,
				priority: graphContextPriority(snippet, "incoming"),
			})

			if item.depth < 1 {
				queue = append(queue, queueItem{path: ref.Path, depth: item.depth + 1})
			}
		}
	}
	return candidates
}

func symbolGraphContextCandidates(db *sql.DB, seed Snippet) []graphContextCandidate {
	var candidates []graphContextCandidate
	candidates = append(candidates, symbolReferenceCandidatesForDefinitions(db, seed, maxSymbolGraphCandidatesPerSeed)...)
	if len(candidates) < maxSymbolGraphCandidatesPerSeed {
		candidates = append(candidates, definitionCandidatesForReferences(db, seed, maxSymbolGraphCandidatesPerSeed-len(candidates))...)
	}
	return candidates
}

func symbolReferenceCandidatesForDefinitions(db *sql.DB, seed Snippet, limit int) []graphContextCandidate {
	if limit <= 0 {
		return nil
	}

	var candidates []graphContextCandidate
	for _, symbol := range limitSymbols(symbolsInSnippet(db, seed), maxSymbolGraphNamesPerSeed) {
		refs, err := symbolReferencesForName(db, symbol.Name, symbol.Language, maxSymbolGraphRefsPerName)
		if err != nil {
			continue
		}
		for _, ref := range refs {
			if len(candidates) >= limit {
				return candidates
			}
			if ref.Path == "" || ref.Path == seed.Path {
				continue
			}
			snippet, ok, err := indexedSnippetAtLine(db, ref.Path, ref.Line, 24)
			if err != nil || !ok {
				continue
			}
			snippet.Score = minFloat(snippet.Score, -0.66)
			evidence := fmt.Sprintf("%s:%d references %s defined in %s", ref.Path, ref.Line, symbol.Name, seed.Path)
			receipt := receiptForSnippet(0, snippet, 0.70, []string{
				"included through local symbol reference graph",
				"references a symbol defined by a retrieved snippet",
			}, []string{evidence})
			candidates = append(candidates, graphContextCandidate{
				snippet:  snippet,
				receipt:  receipt,
				priority: graphContextPriorityForRelation(snippet, "symbol_reference"),
			})
		}
	}
	return candidates
}

func definitionCandidatesForReferences(db *sql.DB, seed Snippet, limit int) []graphContextCandidate {
	if limit <= 0 {
		return nil
	}

	var candidates []graphContextCandidate
	for _, ref := range limitSymbolReferences(symbolReferencesInSnippet(db, seed), maxSymbolGraphNamesPerSeed) {
		definitions, err := symbolsForName(db, ref.Name, ref.Language, maxSymbolGraphRefsPerName)
		if err != nil {
			continue
		}
		for _, symbol := range definitions {
			if len(candidates) >= limit {
				return candidates
			}
			if symbol.Path == "" || symbol.Path == seed.Path {
				continue
			}
			snippet, ok, err := indexedSnippetAtLine(db, symbol.Path, symbol.Line, 18)
			if err != nil || !ok {
				continue
			}
			snippet.Score = minFloat(snippet.Score, -0.69)
			evidence := fmt.Sprintf("%s:%d references %s -> %s:%d", seed.Path, ref.Line, ref.Name, symbol.Path, symbol.Line)
			receipt := receiptForSnippet(0, snippet, 0.72, []string{
				"included through local symbol reference graph",
				"defines a symbol referenced by a retrieved snippet",
			}, []string{evidence})
			candidates = append(candidates, graphContextCandidate{
				snippet:  snippet,
				receipt:  receipt,
				priority: graphContextPriorityForRelation(snippet, "symbol_definition"),
			})
		}
	}
	return candidates
}

func symbolsInSnippet(db *sql.DB, snippet Snippet) []Symbol {
	if db != nil && snippet.Path != "" {
		symbols, err := symbolsForPathRange(db, snippet.Path, snippet.StartLine, snippet.EndLine, 32)
		if err == nil && len(symbols) > 0 {
			return symbols
		}
	}
	return ExtractSymbols(snippet.Language, snippet.Path, snippet.Content)
}

func symbolReferencesInSnippet(db *sql.DB, snippet Snippet) []SymbolReference {
	if db != nil && snippet.Path != "" {
		refs, err := symbolReferencesForPathRange(db, snippet.Path, snippet.StartLine, snippet.EndLine, 32)
		if err == nil && len(refs) > 0 {
			return refs
		}
	}
	return ExtractSymbolReferences(snippet.Language, snippet.Path, snippet.Content)
}

func symbolsForPathRange(db *sql.DB, path string, startLine int, endLine int, limit int) ([]Symbol, error) {
	path = normalizeIndexedPath(path)
	limit = normalizeResultLimit(limit, 32)
	if path == "" {
		return nil, nil
	}

	query := `
		SELECT id, name, kind, signature, language, path, line, docstring
		FROM symbols
		WHERE path = ?`
	args := []any{path}
	if startLine > 0 && endLine >= startLine {
		query += " AND line >= ? AND line <= ?"
		args = append(args, startLine, endLine)
	}
	query += " ORDER BY line ASC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []Symbol
	for rows.Next() {
		symbol, err := scanSymbol(rows)
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, symbol)
	}
	return symbols, rows.Err()
}

func symbolReferencesForPathRange(db *sql.DB, path string, startLine int, endLine int, limit int) ([]SymbolReference, error) {
	path = normalizeIndexedPath(path)
	limit = normalizeResultLimit(limit, 32)
	if path == "" {
		return nil, nil
	}

	query := `
		SELECT id, name, language, path, line, context
		FROM symbol_refs
		WHERE path = ?`
	args := []any{path}
	if startLine > 0 && endLine >= startLine {
		query += " AND line >= ? AND line <= ?"
		args = append(args, startLine, endLine)
	}
	query += " ORDER BY line ASC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []SymbolReference
	for rows.Next() {
		ref, err := scanSymbolReference(rows)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func symbolReferencesForName(db *sql.DB, name string, language string, limit int) ([]SymbolReference, error) {
	name = strings.TrimSpace(name)
	language = NormalizeLanguage(language)
	limit = normalizeResultLimit(limit, maxSymbolGraphRefsPerName)
	if name == "" {
		return nil, nil
	}

	rows, err := db.Query(`
		SELECT id, name, language, path, line, context
		FROM symbol_refs
		WHERE name = ? COLLATE NOCASE
		ORDER BY
			CASE WHEN language = ? THEN 0 ELSE 1 END,
			path ASC,
			line ASC
		LIMIT ?`, name, language, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []SymbolReference
	for rows.Next() {
		ref, err := scanSymbolReference(rows)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func symbolsForName(db *sql.DB, name string, language string, limit int) ([]Symbol, error) {
	name = strings.TrimSpace(name)
	language = NormalizeLanguage(language)
	limit = normalizeResultLimit(limit, maxSymbolGraphRefsPerName)
	if name == "" {
		return nil, nil
	}

	rows, err := db.Query(`
		SELECT id, name, kind, signature, language, path, line, docstring
		FROM symbols
		WHERE name = ? COLLATE NOCASE
		ORDER BY
			CASE WHEN language = ? THEN 0 ELSE 1 END,
			CASE kind
				WHEN 'class' THEN 0
				WHEN 'type' THEN 0
				WHEN 'function' THEN 1
				WHEN 'method' THEN 2
				ELSE 3
			END,
			path ASC,
			line ASC
		LIMIT ?`, name, language, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []Symbol
	for rows.Next() {
		symbol, err := scanSymbol(rows)
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, symbol)
	}
	return symbols, rows.Err()
}

func limitSymbols(symbols []Symbol, limit int) []Symbol {
	if limit <= 0 || len(symbols) <= limit {
		return symbols
	}
	return symbols[:limit]
}

func limitSymbolReferences(refs []SymbolReference, limit int) []SymbolReference {
	if limit <= 0 || len(refs) <= limit {
		return refs
	}
	return refs[:limit]
}

func graphContextPriority(snippet Snippet, direction string) int {
	return graphContextPriorityForRelation(snippet, direction)
}

func graphContextPriorityForRelation(snippet Snippet, relation string) int {
	score := 50
	switch relation {
	case "incoming":
		score += 15
	case "symbol_reference":
		score += 18
	case "symbol_definition":
		score += 22
	case "dependent_type":
		score += 26
	}
	if isTestPath(snippet.Path) {
		score += 20
	}
	if isDependencyPath(snippet.Path) {
		score -= 30
	}
	return score
}

func sortGraphContextCandidates(candidates []graphContextCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority == candidates[j].priority {
			return candidates[i].snippet.Path < candidates[j].snippet.Path
		}
		return candidates[i].priority > candidates[j].priority
	})
}

func dedupeGraphContextCandidates(candidates []graphContextCandidate) []graphContextCandidate {
	seen := map[string]int{}
	result := make([]graphContextCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		key := snippetDedupeKey(candidate.snippet)
		if existing, ok := seen[key]; ok {
			result[existing].priority = max(result[existing].priority, candidate.priority)
			result[existing].receipt.Reasons = uniqueStrings(append(result[existing].receipt.Reasons, candidate.receipt.Reasons...))
			result[existing].receipt.Evidence = uniqueStrings(append(result[existing].receipt.Evidence, candidate.receipt.Evidence...))
			result[existing].receipt.Confidence = maxFloat(result[existing].receipt.Confidence, candidate.receipt.Confidence)
			continue
		}
		seen[key] = len(result)
		result = append(result, candidate)
	}
	sortGraphContextCandidates(result)
	return result
}

func alignReceiptsWithSnippets(snippets []Snippet, receipts []ContextReceipt) []ContextReceipt {
	if len(snippets) == 0 {
		return nil
	}
	byKey := receiptsBySnippetKey(receipts)

	aligned := make([]ContextReceipt, 0, len(snippets))
	for idx, snippet := range snippets {
		receipt, ok := byKey[snippetReceiptKey(snippet)]
		if !ok {
			receipt = receiptForSnippet(idx+1, snippet, 0.45, []string{"included in final context pack"}, nil)
		}
		receipt.Rank = idx + 1
		aligned = append(aligned, receipt)
	}
	return aligned
}

func receiptsBySnippetKey(receipts []ContextReceipt) map[string]ContextReceipt {
	byKey := make(map[string]ContextReceipt, len(receipts))
	for _, receipt := range receipts {
		byKey[receipt.Path+":"+fmt.Sprint(receipt.StartLine)+":"+fmt.Sprint(receipt.EndLine)] = receipt
	}
	return byKey
}

func snippetReceiptKey(snippet Snippet) string {
	return snippet.Path + ":" + fmt.Sprint(snippet.StartLine) + ":" + fmt.Sprint(snippet.EndLine)
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

var pascalCasePattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]*\b`)

func dependentTypeContextCandidates(db *sql.DB, seed Snippet) []graphContextCandidate {
	if db == nil || seed.Path == "" {
		return nil
	}

	var candidates []graphContextCandidate
	seenKeys := map[string]bool{}

	resolveDirectTypes := func(sourcePath string, startLine, endLine int, content string, isTransitive bool) []Snippet {
		var resolved []Snippet

		// 1. Fetch local types in the same file
		localRows, err := db.Query(`
			SELECT name, line, kind FROM symbols
			WHERE path = ? AND kind IN ('class', 'type', 'interface')`, sourcePath)
		if err == nil {
			defer localRows.Close()
			for localRows.Next() {
				var name string
				var line int
				var kind string
				if err := localRows.Scan(&name, &line, &kind); err == nil {
					if line > 0 && (isTransitive || (line < startLine || line > endLine)) {
						wordRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
						if wordRe.MatchString(content) {
							snippet, ok, err := indexedSnippetAtLine(db, sourcePath, line, 24)
							if err == nil && ok {
								key := snippetDedupeKey(snippet)
								if !seenKeys[key] {
									seenKeys[key] = true

									scoreBoost := -0.75
									confidence := 0.85
									reasons := []string{
										"included through dependent type expansion",
										"defines a type/class used in a retrieved snippet",
									}
									evidence := fmt.Sprintf("%s:%d uses local type %s defined at line %d", sourcePath, startLine, name, line)
									if isTransitive {
										scoreBoost = -0.62
										confidence = 0.65
										reasons = append(reasons, "included as a transitive (depth 1) dependent type")
										evidence = fmt.Sprintf("[transitive] %s:%d uses local type %s defined at line %d", sourcePath, startLine, name, line)
									}

									snippet.Score = minFloat(snippet.Score, scoreBoost)
									receipt := receiptForSnippet(0, snippet, confidence, reasons, []string{evidence})
									candidates = append(candidates, graphContextCandidate{
										snippet:  snippet,
										receipt:  receipt,
										priority: graphContextPriorityForRelation(snippet, "dependent_type"),
									})
									resolved = append(resolved, snippet)
								}
							}
						}
					}
				}
			}
		}

		// 2. Scan content for global/imported PascalCase type identifiers
		words := pascalCasePattern.FindAllString(content, -1)
		uniqueWords := uniqueStrings(words)

		globalCount := 0
		for _, word := range uniqueWords {
			if len(word) <= 2 || isCommonGenericWord(word) {
				continue
			}
			limitVal := 2
			if isTransitive {
				limitVal = 1
			}
			if globalCount >= 3 {
				break
			}

			globalRows, err := db.Query(`
				SELECT name, line, kind, path FROM symbols
				WHERE name = ? AND kind IN ('class', 'type', 'interface') AND path != ?
				LIMIT ?`, word, sourcePath, limitVal)
			if err == nil {
				defer globalRows.Close()
				for globalRows.Next() {
					var name string
					var line int
					var kind string
					var path string
					if err := globalRows.Scan(&name, &line, &kind, &path); err == nil {
						snippet, ok, err := indexedSnippetAtLine(db, path, line, 24)
						if err == nil && ok {
							key := snippetDedupeKey(snippet)
							if !seenKeys[key] {
								seenKeys[key] = true
								globalCount++

								scoreBoost := -0.74
								confidence := 0.82
								reasons := []string{
									"included through dependent type expansion",
									"defines an external type/class referenced by a retrieved snippet",
								}
								evidence := fmt.Sprintf("%s:%d references external type %s defined in %s:%d", sourcePath, startLine, name, path, line)
								if isTransitive {
									scoreBoost = -0.61
									confidence = 0.62
									reasons = append(reasons, "included as a transitive (depth 1) dependent type")
									evidence = fmt.Sprintf("[transitive] %s:%d references external type %s defined in %s:%d", sourcePath, startLine, name, path, line)
								}

								snippet.Score = minFloat(snippet.Score, scoreBoost)
								receipt := receiptForSnippet(0, snippet, confidence, reasons, []string{evidence})
								candidates = append(candidates, graphContextCandidate{
									snippet:  snippet,
									receipt:  receipt,
									priority: graphContextPriorityForRelation(snippet, "dependent_type"),
								})
								resolved = append(resolved, snippet)
							}
						}
					}
				}
			}
		}

		return resolved
	}

	directDeps := resolveDirectTypes(seed.Path, seed.StartLine, seed.EndLine, seed.Content, false)
	for _, dep := range directDeps {
		resolveDirectTypes(dep.Path, dep.StartLine, dep.EndLine, dep.Content, true)
	}

	return candidates
}

func isCommonGenericWord(word string) bool {
	switch word {
	case "JSON", "HTTP", "URL", "URI", "API", "GET", "POST", "PUT", "DELETE", "HTML", "XML", "YAML", "SDK", "CLI", "DB", "SQL", "ID", "UID", "UUID", "OK", "ERR", "EOF", "nil", "true", "false", "NULL":
		return true
	default:
		return false
	}
}
