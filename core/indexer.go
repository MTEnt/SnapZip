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

type ContextBundle struct {
	Data       []byte
	Vocabulary []string
	FileCount  int
}

func IndexDirectory(db *sql.DB, root string, filter LanguageFilter) (int, error) {
	indexed := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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

	vocab := make(map[string]bool)
	var builder strings.Builder
	fileCount := 0

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
