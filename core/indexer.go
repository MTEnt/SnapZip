package core

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	"memory.db",
)

type IndexOptions struct {
	MaxFileBytes    int64
	MaxContentBytes int
	SkipDirs        map[string]bool
	SkipFiles       map[string]bool
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
	indexed := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skip, err := shouldSkipEntry(path, entry, options); skip || err != nil {
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
	return indexed, err
}

func IndexFilesWithOptions(db *sql.DB, root string, paths []string, filter LanguageFilter, options IndexOptions) (int, error) {
	options = normalizeIndexOptions(options)
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
	return chunks, nil
}

func LoadContextDirectory(root string, filter LanguageFilter, maxBytes int) (ContextBundle, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultContextLimitBytes
	}
	options := DefaultIndexOptions()

	vocab := make(map[string]bool)
	var builder strings.Builder
	fileCount := 0

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skip, err := shouldSkipEntry(path, entry, options); skip || err != nil {
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
	chunks := splitContentChunks(content, maxContentBytes)
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

func shouldSkipEntry(path string, entry fs.DirEntry, options IndexOptions) (bool, error) {
	base := strings.ToLower(entry.Name())
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
