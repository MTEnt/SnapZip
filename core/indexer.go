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
	MaxFileBytes int64
	SkipDirs     map[string]bool
	SkipFiles    map[string]bool
}

type ContextBundle struct {
	Data       []byte
	Vocabulary []string
	FileCount  int
}

func DefaultIndexOptions() IndexOptions {
	return IndexOptions{
		MaxFileBytes: DefaultMaxIndexFileBytes,
		SkipDirs:     copyBoolMap(defaultSkipDirs),
		SkipFiles:    copyBoolMap(defaultSkipFiles),
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

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !isTextContent(content) {
			return nil
		}
		if err := AddKnowledge(db, language, topicForPath(root, path), string(content)); err != nil {
			return err
		}
		indexed++
		return nil
	})
	return indexed, err
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
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		rel = filepath.Base(path)
	}
	return fmt.Sprintf("Source file: %s", rel)
}

func normalizeIndexOptions(options IndexOptions) IndexOptions {
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = DefaultMaxIndexFileBytes
	}
	if options.SkipDirs == nil {
		options.SkipDirs = copyBoolMap(defaultSkipDirs)
	}
	if options.SkipFiles == nil {
		options.SkipFiles = copyBoolMap(defaultSkipFiles)
	}
	return options
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
