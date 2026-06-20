package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshDatabaseHasNoFeedback(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	feedback, err := RetrieveNegativeFeedback(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 0 {
		t.Fatalf("fresh database returned %d feedback rows, want 0", len(feedback))
	}
}

func TestIndexDirectoryUsesLanguageAliasesAndRelativeTopics(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pkg/cache.py", "class BoundedCache:\n    pass\n")
	writeTestFile(t, root, "pkg/cache.go", "package cache\n")
	writeTestFile(t, root, "notes.txt", "not source code")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	count, err := IndexDirectory(db, root, NewLanguageFilter("python"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("indexed %d files, want 1", count)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "python BoundedCache", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Language != "py" {
		t.Fatalf("got language %q, want py", results[0].Language)
	}
	if results[0].Topic != "Source file: pkg/cache.py" {
		t.Fatalf("got topic %q", results[0].Topic)
	}
}

func TestIndexDirectorySkipsGeneratedLargeAndBinaryFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pkg/cache.py", "class BoundedCache:\n    pass\n")
	writeTestFile(t, root, "node_modules/package/ignored.py", "class ShouldNotIndex:\n    pass\n")
	writeTestFile(t, root, "pkg/large.py", strings.Repeat("x", int(DefaultMaxIndexFileBytes)+1))
	writeTestFile(t, root, "pkg/binary.py", "valid text\x00binary tail")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	count, err := IndexDirectory(db, root, NewLanguageFilter("python"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("indexed %d files, want 1", count)
	}

	stats, err := GetDatabaseStats(db)
	if err != nil {
		t.Fatal(err)
	}
	if stats.KnowledgeRows != 1 {
		t.Fatalf("stored %d knowledge rows, want 1", stats.KnowledgeRows)
	}
}

func TestIndexDirectoryUpdatesExistingRows(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pkg/cache.py", "class BoundedCache:\n    pass\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for i := 0; i < 2; i++ {
		count, err := IndexDirectory(db, root, NewLanguageFilter("python"))
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("indexed %d files on pass %d, want 1", count, i+1)
		}
	}

	stats, err := GetDatabaseStats(db)
	if err != nil {
		t.Fatal(err)
	}
	if stats.KnowledgeRows != 1 {
		t.Fatalf("stored %d knowledge rows after duplicate indexing, want 1", stats.KnowledgeRows)
	}
}

func TestIndexDirectoryChunksLargeFiles(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("def helper():\n    return 'chunked search content'\n", 200)
	writeTestFile(t, root, "pkg/large.py", content)

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	options := DefaultIndexOptions()
	options.MaxFileBytes = int64(len(content) + 1024)
	options.MaxContentBytes = 1024

	count, err := IndexDirectoryWithOptions(db, root, NewLanguageFilter("python"), options)
	if err != nil {
		t.Fatal(err)
	}
	if count <= 1 {
		t.Fatalf("indexed %d chunks, want more than 1", count)
	}

	stats, err := GetDatabaseStats(db)
	if err != nil {
		t.Fatal(err)
	}
	if stats.KnowledgeRows != count {
		t.Fatalf("stored %d rows, want %d", stats.KnowledgeRows, count)
	}
}

func TestLoadContextDirectoryRespectsLanguageFilter(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "cache.py", "class BoundedCache:\n    pass\n")
	writeTestFile(t, root, "cache.go", "package cache\n")

	context, err := LoadContextDirectory(root, NewLanguageFilter("go"), DefaultContextLimitBytes)
	if err != nil {
		t.Fatal(err)
	}
	if context.FileCount != 1 {
		t.Fatalf("loaded %d files, want 1", context.FileCount)
	}
	if string(context.Data) != "package cache\n\n" {
		t.Fatalf("unexpected context data %q", string(context.Data))
	}
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
