package core

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultContextLimitBytes = 2 * 1024 * 1024
const DefaultMaxIndexFileBytes int64 = 1024 * 1024
const DefaultMaxKnowledgeContentBytes = 64 * 1024

var defaultSkipDirs = stringSet(
	".cache",
	".git",
	".hg",
	".idea",
	".next",
	".svn",
	".turbo",
	".venv",
	".vscode",
	"build",
	"coverage",
	"dist",
	"node_modules",
	"out",
	"target",
	"vendor",
	"venv",
	"__pycache__",
)

var defaultSkipFiles = stringSet(
	".ds_store",
	".snapzipignore",
	"memory.db",
)

type IndexOptions struct {
	MaxFileBytes    int64
	MaxContentBytes int
	SkipDirs        map[string]bool
	SkipFiles       map[string]bool
	IgnorePatterns  []string
}

type ContextBundle struct {
	Data       []byte
	Vocabulary []string
	FileCount  int
}

func DefaultIndexOptions() IndexOptions {
	return IndexOptions{
		MaxFileBytes:    DefaultMaxIndexFileBytes,
		MaxContentBytes: DefaultMaxKnowledgeContentBytes,
		SkipDirs:        copyBoolMap(defaultSkipDirs),
		SkipFiles:       copyBoolMap(defaultSkipFiles),
	}
}

func IndexDirectory(db *sql.DB, root string, filter LanguageFilter) (int, error) {
	return IndexDirectoryWithOptions(db, root, filter, DefaultIndexOptions())
}

func IndexDirectoryWithOptions(db *sql.DB, root string, filter LanguageFilter, options IndexOptions) (int, error) {
	options = normalizeIndexOptions(options)
	options = withSnapZipIgnore(root, options)
	indexed := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skip, err := shouldSkipEntry(root, path, entry, options); skip || err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		language := LanguageFromPath(path)
		if !filter.Matches(language) {
			return nil
		}

		chunks, err := IndexFileWithOptions(db, root, path, filter, options)
		if err != nil {
			return err
		}
		indexed += chunks
		return nil
	})
	if err != nil {
		return indexed, err
	}
	if err := ResolveImportTargets(db); err != nil {
		return indexed, err
	}
	return indexed, nil
}

func IndexFilesWithOptions(db *sql.DB, root string, paths []string, filter LanguageFilter, options IndexOptions) (int, error) {
	options = normalizeIndexOptions(options)
	options = withSnapZipIgnore(root, options)
	indexed := 0
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		chunks, err := IndexFileWithOptions(db, root, path, filter, options)
		if err != nil {
			return indexed, err
		}
		indexed += chunks
	}
	if err := ResolveImportTargets(db); err != nil {
		return indexed, err
	}
	return indexed, nil
}

func IndexFileWithOptions(db *sql.DB, root, path string, filter LanguageFilter, options IndexOptions) (int, error) {
	options = normalizeIndexOptions(options)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir() || info.Size() > options.MaxFileBytes {
		return 0, nil
	}
	relPath := relativeSourcePath(root, path)
	if matchesIgnorePatterns(relPath, options.IgnorePatterns) {
		return 0, nil
	}
	if options.SkipFiles[strings.ToLower(filepath.Base(relPath))] {
		return 0, nil
	}
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		if options.SkipDirs[strings.ToLower(part)] {
			return 0, nil
		}
	}

	language := LanguageFromPath(path)
	if !filter.Matches(language) {
		return 0, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if !isTextContent(content) {
		return 0, nil
	}

	chunks, err := AddKnowledgeContent(db, language, topicForPath(root, path), relPath, content, options.MaxContentBytes, info.ModTime().Unix())
	if err != nil {
		return 0, err
	}
	if err := ReplaceSymbolsForFile(db, language, relPath, content); err != nil {
		return 0, err
	}
	if err := ReplaceSymbolReferencesForFile(db, language, relPath, content); err != nil {
		return 0, err
	}
	if err := ReplaceImportsForFile(db, language, relPath, content); err != nil {
		return 0, err
	}
	return chunks, nil
}

func LoadContextDirectory(root string, filter LanguageFilter, maxBytes int) (ContextBundle, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultContextLimitBytes
	}
	options := DefaultIndexOptions()
	options = withSnapZipIgnore(root, options)

	vocab := make(map[string]bool)
	var builder strings.Builder
	fileCount := 0

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skip, err := shouldSkipEntry(root, path, entry, options); skip || err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if builder.Len() >= maxBytes {
			return nil
		}

		language := LanguageFromPath(path)
		if !filter.Matches(language) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !isTextContent(content) {
			return nil
		}
		writeLimited(&builder, content, maxBytes)
		if builder.Len() < maxBytes {
			builder.WriteByte('\n')
		}
		fileCount++

		for _, word := range strings.Fields(string(content)) {
			if len(word) > 3 && len(word) < 20 {
				vocab[word] = true
			}
		}
		return nil
	})
	if err != nil {
		return ContextBundle{}, err
	}

	vocabulary := make([]string, 0, len(vocab))
	for word := range vocab {
		vocabulary = append(vocabulary, word)
	}
	return ContextBundle{
		Data:       []byte(builder.String()),
		Vocabulary: vocabulary,
		FileCount:  fileCount,
	}, nil
}

func writeLimited(builder *strings.Builder, content []byte, maxBytes int) {
	remaining := maxBytes - builder.Len()
	if remaining <= 0 {
		return
	}
	if len(content) > remaining {
		content = content[:remaining]
	}
	builder.Write(content)
}

func topicForPath(root, path string) string {
	return fmt.Sprintf("Source file: %s", relativeSourcePath(root, path))
}

func relativeSourcePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		rel = filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

func AddKnowledgeContent(db *sql.DB, language, topic, path string, content []byte, maxContentBytes int, sourceMTime int64) (int, error) {
	chunks := splitContentChunksForLanguage(language, content, maxContentBytes)
	for idx, chunk := range chunks {
		chunkTopic := topic
		if len(chunks) > 1 {
			chunkTopic = fmt.Sprintf("%s #chunk-%03d", topic, idx+1)
		}
		_, err := AddKnowledgeEntry(db, KnowledgeEntry{
			Language:    language,
			Topic:       chunkTopic,
			Path:        path,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Content:     string(chunk.Data),
			ContentHash: contentHash(chunk.Data),
			SourceMTime: sourceMTime,
		})
		if err != nil {
			return idx, err
		}
	}
	return len(chunks), nil
}

func splitContentChunksForLanguage(language string, content []byte, maxBytes int) []contentChunk {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return splitContentChunks(content, maxBytes)
	}

	language = NormalizeLanguage(language)
	if chunks, ok := splitStructuralContentChunks(language, content, maxBytes); ok {
		return chunks
	}

	lines := splitLinesAfter(string(content))
	boundaries := codeBoundaryLineIndexes(language, string(content), lines)
	if len(boundaries) == 0 {
		return splitContentChunks(content, maxBytes)
	}

	boundaries = append([]int{0}, boundaries...)
	boundaries = append(uniqueIndexes(boundaries, len(lines)), len(lines))
	if len(boundaries) <= 2 {
		return splitContentChunks(content, maxBytes)
	}

	var chunks []contentChunk
	var current strings.Builder
	currentStartLine := 0

	flushCurrent := func() {
		if current.Len() == 0 {
			return
		}
		data := []byte(current.String())
		chunks = append(chunks, contentChunk{
			Data:      data,
			StartLine: currentStartLine + 1,
			EndLine:   currentStartLine + lineCount(data),
		})
		current.Reset()
		currentStartLine = 0
	}

	for idx := 0; idx < len(boundaries)-1; idx++ {
		start := boundaries[idx]
		end := boundaries[idx+1]
		if start >= end {
			continue
		}
		block := strings.Join(lines[start:end], "")
		if block == "" {
			continue
		}

		if len([]byte(block)) > maxBytes {
			flushCurrent()
			for _, chunk := range splitContentChunks([]byte(block), maxBytes) {
				chunk.StartLine += start
				chunk.EndLine += start
				chunks = append(chunks, chunk)
			}
			continue
		}

		if current.Len() > 0 && current.Len()+len([]byte(block)) > maxBytes {
			flushCurrent()
		}
		if current.Len() == 0 {
			currentStartLine = start
		}
		current.WriteString(block)
	}
	flushCurrent()

	if len(chunks) == 0 {
		return splitContentChunks(content, maxBytes)
	}
	return chunks
}

func splitStructuralContentChunks(language string, content []byte, maxBytes int) ([]contentChunk, bool) {
	lines := splitLinesAfter(string(content))
	spans, ok := codeSpansForLanguage(language, string(content))
	if !ok || len(spans) == 0 {
		return nil, false
	}

	blocks := codeBlocksFromSpans(lines, spans)
	if len(blocks) <= 1 {
		return nil, false
	}
	chunks := packCodeBlocks(blocks, maxBytes)
	if len(chunks) == 0 {
		return nil, false
	}
	return chunks, true
}

func splitLinesAfter(content string) []string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}

type codeSpan struct {
	StartLine int
	EndLine   int
}

func codeSpansForLanguage(language, content string) ([]codeSpan, bool) {
	switch NormalizeLanguage(language) {
	case "go":
		return extractGoTopLevelSpansAST("", content)
	case "py":
		return extractPythonTopLevelSpans(content), true
	case "js", "jsx", "ts", "tsx", "mjs", "cjs", "java", "cs", "c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hxx", "rs", "php", "swift", "kt", "kts", "scala", "sc":
		return extractBraceTopLevelSpans(language, content), true
	case "rb":
		return extractRubyTopLevelSpans(content), true
	default:
		return nil, false
	}
}

func extractPythonTopLevelSpans(content string) []codeSpan {
	lines := splitLinesAfter(content)
	var spans []codeSpan
	for idx := 0; idx < len(lines); {
		if !isPythonTopLevelStatement(lines[idx]) {
			idx++
			continue
		}

		startIdx := pythonDecoratorStartIndex(lines, idx)
		endIdx := pythonBlockEndIndex(lines, idx)
		spans = append(spans, codeSpan{StartLine: startIdx + 1, EndLine: endIdx + 1})
		idx = endIdx + 1
	}
	return spans
}

func isPythonTopLevelStatement(line string) bool {
	if leadingIndentWidth(line) != 0 {
		return false
	}
	trimmed := strings.TrimSpace(stripLineComment("py", line))
	if trimmed == "" || strings.HasPrefix(trimmed, "@") {
		return false
	}
	return !startsWithAny(trimmed, ")", "]", "}", "else:", "elif ", "except ", "finally:")
}

func pythonDecoratorStartIndex(lines []string, idx int) int {
	indent := leadingIndentWidth(lines[idx])
	start := idx
	for start > 0 {
		prev := strings.TrimSpace(stripLineComment("py", lines[start-1]))
		if prev == "" || !strings.HasPrefix(prev, "@") || leadingIndentWidth(lines[start-1]) != indent {
			break
		}
		start--
	}
	return start
}

func pythonBlockEndIndex(lines []string, startIdx int) int {
	indent := leadingIndentWidth(lines[startIdx])
	lastContent := startIdx
	bracketDepth := max(0, bracketDepthDelta(stripLineComment("py", lines[startIdx])))

	for idx := startIdx + 1; idx < len(lines); idx++ {
		trimmed := strings.TrimSpace(stripLineComment("py", lines[idx]))
		if trimmed == "" {
			continue
		}

		lineIndent := leadingIndentWidth(lines[idx])
		if bracketDepth == 0 && lineIndent <= indent && !isPythonContinuationClause(trimmed) {
			return lastContent
		}

		lastContent = idx
		bracketDepth += bracketDepthDelta(stripLineComment("py", lines[idx]))
		if bracketDepth < 0 {
			bracketDepth = 0
		}
	}
	return lastContent
}

func isPythonContinuationClause(trimmed string) bool {
	return startsWithAny(trimmed, "else:", "elif ", "except ", "finally:")
}

func extractBraceTopLevelSpans(language, content string) []codeSpan {
	language = NormalizeLanguage(language)
	lines := splitLinesAfter(content)
	var spans []codeSpan
	for idx := 0; idx < len(lines); {
		if !isBraceTopLevelStart(language, lines[idx]) {
			idx++
			continue
		}

		startIdx := codeDecorationStartIndex(language, lines, idx)
		endIdx := braceBlockEndIndex(language, lines, idx)
		spans = append(spans, codeSpan{StartLine: startIdx + 1, EndLine: endIdx + 1})
		idx = endIdx + 1
	}
	return spans
}

func isBraceTopLevelStart(language, line string) bool {
	trimmed := strings.TrimSpace(stripLineComment(language, line))
	if trimmed == "" || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "*/") {
		return false
	}
	normalized := NormalizeLanguage(language)
	trimmed = trimCodeModifiers(trimmed, braceLanguageModifiers(normalized)...)

	switch normalized {
	case "js", "jsx", "ts", "tsx", "mjs", "cjs":
		if startsWithAny(trimmed, "class ", "function ", "interface ", "enum ", "namespace ", "module ") {
			return true
		}
		if startsWithAny(trimmed, "type ", "const ", "let ", "var ") {
			return true
		}
	case "java", "cs":
		return startsWithAny(trimmed, "class ", "interface ", "enum ", "record ", "struct ")
	case "c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hxx":
		if startsWithAny(trimmed, "class ", "struct ", "enum ", "namespace ", "template", "extern \"C\"") {
			return true
		}
		return looksLikeBraceLanguageFunctionStart(trimmed)
	case "rs":
		return startsWithAny(trimmed, "fn ", "struct ", "enum ", "trait ", "impl ", "mod ", "type ", "const ", "static ")
	case "php":
		return startsWithAny(trimmed, "<?php", "class ", "interface ", "trait ", "enum ", "function ")
	case "swift":
		return startsWithAny(trimmed, "class ", "struct ", "enum ", "protocol ", "extension ", "func ", "actor ")
	case "kt", "kts":
		return startsWithAny(trimmed, "class ", "interface ", "object ", "data class ", "sealed class ", "fun ", "typealias ")
	case "scala", "sc":
		return startsWithAny(trimmed, "class ", "object ", "trait ", "enum ", "case class ", "def ", "type ", "given ")
	}
	return false
}

func braceLanguageModifiers(language string) []string {
	switch NormalizeLanguage(language) {
	case "js", "jsx", "ts", "tsx", "mjs", "cjs":
		return []string{"export ", "default ", "declare ", "async "}
	case "java":
		return []string{"public ", "private ", "protected ", "static ", "final ", "abstract ", "sealed ", "non-sealed ", "strictfp "}
	case "cs":
		return []string{"public ", "private ", "protected ", "internal ", "static ", "sealed ", "abstract ", "partial ", "async ", "unsafe ", "readonly "}
	case "c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hxx":
		return []string{"inline ", "static ", "extern ", "constexpr ", "virtual ", "template<> "}
	case "rs":
		return []string{"pub ", "pub(crate) ", "async ", "unsafe "}
	case "php":
		return []string{"<?php ", "abstract ", "final ", "public ", "private ", "protected ", "static "}
	case "swift":
		return []string{"public ", "private ", "fileprivate ", "internal ", "open ", "static ", "final "}
	case "kt", "kts":
		return []string{"public ", "private ", "protected ", "internal ", "open ", "abstract ", "sealed ", "data ", "inline ", "suspend ", "operator "}
	case "scala", "sc":
		return []string{"private ", "protected ", "final ", "sealed ", "abstract ", "implicit ", "inline ", "case "}
	default:
		return nil
	}
}

func looksLikeBraceLanguageFunctionStart(trimmed string) bool {
	if !strings.Contains(trimmed, "(") || !strings.Contains(trimmed, ")") {
		return false
	}
	lower := strings.ToLower(trimmed)
	if startsWithAny(lower, "if ", "for ", "while ", "switch ", "catch ", "return ", "sizeof ") {
		return false
	}
	if strings.Contains(lower, " = ") || strings.HasPrefix(lower, "#") {
		return false
	}
	return strings.Contains(trimmed, "{") || strings.HasSuffix(trimmed, ")") || strings.HasSuffix(trimmed, ") const")
}

func codeDecorationStartIndex(language string, lines []string, idx int) int {
	start := idx
	for start > 0 {
		trimmed := strings.TrimSpace(stripLineComment(language, lines[start-1]))
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(trimmed, "@") || strings.HasPrefix(trimmed, "#[") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			start--
			continue
		}
		break
	}
	return start
}

func braceBlockEndIndex(language string, lines []string, startIdx int) int {
	depth := 0
	opened := false
	lastContent := startIdx
	for idx := startIdx; idx < len(lines); idx++ {
		stripped := stripLineComment(language, lines[idx])
		trimmed := strings.TrimSpace(stripped)
		if trimmed == "" {
			continue
		}
		lastContent = idx

		if lineHasOpeningDelimiter(stripped) {
			opened = true
		}
		depth += bracketDepthDelta(stripped)
		if depth < 0 {
			depth = 0
		}

		if opened && depth == 0 {
			return idx
		}
		if !opened && strings.Contains(trimmed, ";") {
			return idx
		}
	}
	return lastContent
}

func lineHasOpeningDelimiter(line string) bool {
	return strings.ContainsAny(line, "({[")
}

func extractRubyTopLevelSpans(content string) []codeSpan {
	lines := splitLinesAfter(content)
	var spans []codeSpan
	for idx := 0; idx < len(lines); {
		if !isRubyTopLevelStart(lines[idx]) {
			idx++
			continue
		}
		endIdx := rubyBlockEndIndex(lines, idx)
		spans = append(spans, codeSpan{StartLine: idx + 1, EndLine: endIdx + 1})
		idx = endIdx + 1
	}
	return spans
}

func isRubyTopLevelStart(line string) bool {
	if leadingIndentWidth(line) != 0 {
		return false
	}
	trimmed := strings.TrimSpace(stripLineComment("rb", line))
	return startsWithAny(trimmed, "class ", "module ", "def ")
}

func rubyBlockEndIndex(lines []string, startIdx int) int {
	depth := 0
	lastContent := startIdx
	for idx := startIdx; idx < len(lines); idx++ {
		trimmed := strings.TrimSpace(stripLineComment("rb", lines[idx]))
		if trimmed == "" {
			continue
		}
		lastContent = idx
		depth += rubyBlockStartCount(trimmed)
		depth -= rubyEndCount(trimmed)
		if depth <= 0 && idx > startIdx {
			return idx
		}
	}
	return lastContent
}

func rubyBlockStartCount(trimmed string) int {
	count := 0
	if startsWithAny(trimmed, "class ", "module ", "def ", "if ", "unless ", "case ", "begin", "while ", "until ", "for ") {
		count++
	}
	if strings.HasSuffix(trimmed, " do") || strings.Contains(trimmed, " do |") {
		count++
	}
	return count
}

func rubyEndCount(trimmed string) int {
	if trimmed == "end" || strings.HasPrefix(trimmed, "end ") || strings.HasSuffix(trimmed, "; end") {
		return 1
	}
	return 0
}

func bracketDepthDelta(line string) int {
	delta := 0
	for _, char := range line {
		switch char {
		case '(', '[', '{':
			delta++
		case ')', ']', '}':
			delta--
		}
	}
	return delta
}

func codeBlocksFromSpans(lines []string, spans []codeSpan) []contentChunk {
	spans = normalizeCodeSpans(spans, len(lines))
	if len(spans) == 0 {
		return nil
	}

	var blocks []contentChunk
	cursor := 1
	for _, span := range spans {
		startLine := min(cursor, span.StartLine)
		if startLine < 1 {
			startLine = 1
		}
		if span.EndLine < startLine {
			continue
		}
		blocks = append(blocks, contentChunk{
			Data:      []byte(strings.Join(lines[startLine-1:span.EndLine], "")),
			StartLine: startLine,
			EndLine:   span.EndLine,
		})
		cursor = span.EndLine + 1
	}
	if cursor <= len(lines) && len(blocks) > 0 {
		last := &blocks[len(blocks)-1]
		last.Data = append(last.Data, []byte(strings.Join(lines[cursor-1:], ""))...)
		last.EndLine = len(lines)
	}
	return blocks
}

func normalizeCodeSpans(spans []codeSpan, maxLine int) []codeSpan {
	var normalized []codeSpan
	for _, span := range spans {
		if span.StartLine <= 0 || span.EndLine <= 0 {
			continue
		}
		if span.StartLine > maxLine {
			continue
		}
		if span.EndLine > maxLine {
			span.EndLine = maxLine
		}
		if span.EndLine < span.StartLine {
			continue
		}
		normalized = append(normalized, span)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].StartLine == normalized[j].StartLine {
			return normalized[i].EndLine < normalized[j].EndLine
		}
		return normalized[i].StartLine < normalized[j].StartLine
	})

	merged := normalized[:0]
	for _, span := range normalized {
		if len(merged) == 0 || span.StartLine > merged[len(merged)-1].EndLine {
			merged = append(merged, span)
			continue
		}
		if span.EndLine > merged[len(merged)-1].EndLine {
			merged[len(merged)-1].EndLine = span.EndLine
		}
	}
	return merged
}

func packCodeBlocks(blocks []contentChunk, maxBytes int) []contentChunk {
	var chunks []contentChunk
	var current strings.Builder
	currentStartLine := 0
	currentEndLine := 0

	flushCurrent := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, contentChunk{
			Data:      []byte(current.String()),
			StartLine: currentStartLine,
			EndLine:   currentEndLine,
		})
		current.Reset()
		currentStartLine = 0
		currentEndLine = 0
	}

	for _, block := range blocks {
		if len(block.Data) == 0 {
			continue
		}
		if len(block.Data) > maxBytes {
			flushCurrent()
			for _, chunk := range splitContentChunks(block.Data, maxBytes) {
				chunk.StartLine += block.StartLine - 1
				chunk.EndLine += block.StartLine - 1
				chunks = append(chunks, chunk)
			}
			continue
		}

		if current.Len() > 0 && current.Len()+len(block.Data) > maxBytes {
			flushCurrent()
		}
		if current.Len() == 0 {
			currentStartLine = block.StartLine
		}
		current.Write(block.Data)
		currentEndLine = block.EndLine
	}
	flushCurrent()
	return chunks
}

func codeBoundaryLineIndexes(language, content string, lines []string) []int {
	var indexes []int
	for _, symbol := range ExtractSymbols(language, "", content) {
		indexes = append(indexes, symbol.Line-1)
	}
	for idx, line := range lines {
		if isSupplementalCodeBoundaryLine(language, line) {
			indexes = append(indexes, idx)
		}
	}
	return uniqueIndexes(indexes, len(lines))
}

func uniqueIndexes(indexes []int, maxIndex int) []int {
	seen := make(map[int]bool, len(indexes))
	unique := make([]int, 0, len(indexes))
	for _, idx := range indexes {
		if idx < 0 || idx > maxIndex || seen[idx] {
			continue
		}
		seen[idx] = true
		unique = append(unique, idx)
	}
	return unique
}

func isSupplementalCodeBoundaryLine(language, line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
		return false
	}

	switch NormalizeLanguage(language) {
	case "go":
		return startsWithAny(trimmed, "const (", "var (")
	case "js", "jsx", "ts", "tsx", "mjs", "cjs":
		trimmed = trimCodeModifiers(trimmed, "export ", "default ", "declare ")
		return startsWithAny(trimmed, "interface ", "type ", "enum ", "const ", "let ", "var ")
	case "java", "cs":
		trimmed = trimCodeModifiers(trimmed, "public ", "private ", "protected ", "static ", "final ", "abstract ", "async ")
		return startsWithAny(trimmed, "enum ", "record ")
	case "rs":
		trimmed = trimCodeModifiers(trimmed, "pub ", "async ")
		return startsWithAny(trimmed, "impl ")
	case "php":
		trimmed = trimCodeModifiers(trimmed, "public ", "private ", "protected ", "static ", "final ", "abstract ")
		return startsWithAny(trimmed, "interface ", "trait ")
	case "swift":
		trimmed = trimCodeModifiers(trimmed, "public ", "private ", "internal ", "open ", "static ")
		return startsWithAny(trimmed, "extension ")
	default:
		return false
	}
}

func trimCodeModifiers(line string, modifiers ...string) string {
	line = strings.TrimSpace(line)
	for {
		trimmed := line
		for _, modifier := range modifiers {
			trimmed = strings.TrimPrefix(trimmed, modifier)
		}
		if trimmed == line {
			return line
		}
		line = strings.TrimSpace(trimmed)
	}
}

func startsWithAny(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func normalizeIndexOptions(options IndexOptions) IndexOptions {
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = DefaultMaxIndexFileBytes
	}
	if options.MaxContentBytes <= 0 {
		options.MaxContentBytes = DefaultMaxKnowledgeContentBytes
	}
	if options.SkipDirs == nil {
		options.SkipDirs = copyBoolMap(defaultSkipDirs)
	}
	if options.SkipFiles == nil {
		options.SkipFiles = copyBoolMap(defaultSkipFiles)
	}
	return options
}

func withSnapZipIgnore(root string, options IndexOptions) IndexOptions {
	patterns, err := LoadSnapZipIgnore(root)
	if err != nil || len(patterns) == 0 {
		return options
	}
	options.IgnorePatterns = append(append([]string{}, options.IgnorePatterns...), patterns...)
	return options
}

func LoadSnapZipIgnore(root string) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(root, ".snapzipignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var patterns []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(filepath.ToSlash(line), "./")
		if line != "" {
			patterns = append(patterns, strings.ToLower(line))
		}
	}
	return uniqueStrings(patterns), nil
}

type contentChunk struct {
	Data      []byte
	StartLine int
	EndLine   int
}

func splitContentChunks(content []byte, maxBytes int) []contentChunk {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxKnowledgeContentBytes
	}
	if len(content) <= maxBytes {
		return []contentChunk{{
			Data:      content,
			StartLine: 1,
			EndLine:   lineCount(content),
		}}
	}

	var chunks []contentChunk
	startLine := 1
	for start := 0; start < len(content); {
		end := start + maxBytes
		if end >= len(content) {
			chunk := content[start:]
			chunks = append(chunks, contentChunk{
				Data:      chunk,
				StartLine: startLine,
				EndLine:   startLine + lineCount(chunk) - 1,
			})
			break
		}

		chunkEnd := end
		if newline := lastNewline(content[start:end]); newline > maxBytes/2 {
			chunkEnd = start + newline + 1
		}
		chunk := content[start:chunkEnd]
		endLine := startLine + lineCount(chunk) - 1
		chunks = append(chunks, contentChunk{
			Data:      chunk,
			StartLine: startLine,
			EndLine:   endLine,
		})
		startLine = endLine + 1
		start = chunkEnd
	}
	return chunks
}

func lineCount(content []byte) int {
	if len(content) == 0 {
		return 1
	}
	lines := 1
	for _, b := range content {
		if b == '\n' {
			lines++
		}
	}
	if content[len(content)-1] == '\n' && lines > 1 {
		lines--
	}
	return lines
}

func lastNewline(content []byte) int {
	for idx := len(content) - 1; idx >= 0; idx-- {
		if content[idx] == '\n' {
			return idx
		}
	}
	return -1
}

func shouldSkipEntry(root, path string, entry fs.DirEntry, options IndexOptions) (bool, error) {
	base := strings.ToLower(entry.Name())
	relPath := strings.ToLower(relativeSourcePath(root, path))
	if matchesIgnorePatterns(relPath, options.IgnorePatterns) {
		if entry.IsDir() {
			return true, filepath.SkipDir
		}
		return true, nil
	}
	if entry.IsDir() {
		if options.SkipDirs[base] {
			return true, filepath.SkipDir
		}
		return false, nil
	}
	if options.SkipFiles[base] {
		return true, nil
	}

	info, err := entry.Info()
	if err != nil {
		return false, err
	}
	if info.Size() > options.MaxFileBytes {
		return true, nil
	}
	return false, nil
}

func matchesIgnorePatterns(relPath string, patterns []string) bool {
	relPath = strings.ToLower(strings.TrimPrefix(filepath.ToSlash(relPath), "./"))
	base := strings.ToLower(filepath.Base(relPath))
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(pattern)), "./"))
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "/") {
			prefix := strings.TrimSuffix(pattern, "/")
			if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
				return true
			}
			continue
		}
		if !strings.Contains(pattern, "/") {
			if base == pattern {
				return true
			}
			for _, part := range strings.Split(relPath, "/") {
				if part == pattern {
					return true
				}
			}
			continue
		}
		if relPath == pattern || strings.HasPrefix(relPath, pattern+"/") {
			return true
		}
		if ok, _ := filepath.Match(pattern, relPath); ok {
			return true
		}
	}
	return false
}

func isTextContent(content []byte) bool {
	if len(content) == 0 {
		return true
	}

	sampleSize := len(content)
	if sampleSize > 8192 {
		sampleSize = 8192
	}

	controlBytes := 0
	for _, b := range content[:sampleSize] {
		if b == 0 {
			return false
		}
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			controlBytes++
		}
	}
	return controlBytes*100/sampleSize < 30
}

func copyBoolMap(input map[string]bool) map[string]bool {
	output := make(map[string]bool, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
