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
	if results[0].Path != "pkg/cache.py" {
		t.Fatalf("got path %q", results[0].Path)
	}
	if results[0].StartLine != 1 || results[0].EndLine != 2 {
		t.Fatalf("got lines %d-%d, want 1-2", results[0].StartLine, results[0].EndLine)
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

func TestIndexDirectoryRespectsSnapZipIgnore(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".snapzipignore", "private/\nignored.py\n")
	writeTestFile(t, root, "pkg/cache.py", "class BoundedCache:\n    pass\n")
	writeTestFile(t, root, "private/secret.py", "class ShouldNotIndexPrivate:\n    pass\n")
	writeTestFile(t, root, "pkg/ignored.py", "class ShouldNotIndexIgnored:\n    pass\n")

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

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "ShouldNotIndex", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("ignored content was indexed: %+v", results)
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

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "chunked search content", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("got no chunked results")
	}
	for _, result := range results {
		if result.Path != "pkg/large.py" {
			t.Fatalf("got chunk path %q", result.Path)
		}
		if result.StartLine <= 0 || result.EndLine < result.StartLine {
			t.Fatalf("invalid chunk line range %d-%d", result.StartLine, result.EndLine)
		}
	}
}

func TestFindAffectedTestsUsesDirectAndRelatedSignals(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pkg/cache.go", "package cache\n\ntype CacheStore struct{}\n\nfunc NewCacheStore() CacheStore { return CacheStore{} }\n")
	writeTestFile(t, root, "pkg/cache_test.go", "package cache\n\nfunc TestConstructor(t *testing.T) { _ = NewCacheStore() }\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("go")); err != nil {
		t.Fatal(err)
	}
	report, err := FindAffectedTests(db, []string{"pkg/cache.go"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tests) == 0 || report.Tests[0].Path != "pkg/cache_test.go" {
		t.Fatalf("affected tests = %+v, want pkg/cache_test.go", report.Tests)
	}
}

func TestIndexDirectoryStoresSymbolsAndRepoMap(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pkg/cache.go", "package cache\n\ntype CacheStore struct{}\n\nfunc NewCacheStore() CacheStore { return CacheStore{} }\n")
	writeTestFile(t, root, "pkg/cache_test.go", "package cache\n\nfunc TestConstructor(t *testing.T) { _ = NewCacheStore() }\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	count, err := IndexDirectory(db, root, NewLanguageFilter("go"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("indexed %d files, want 2", count)
	}

	symbols, err := SearchSymbols(db, "CacheStore", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) < 2 {
		t.Fatalf("got %d symbols, want at least 2", len(symbols))
	}
	if symbols[0].Path == "" || symbols[0].Line == 0 {
		t.Fatalf("symbol missing source coordinates: %+v", symbols[0])
	}

	repoMap, err := BuildRepoMap(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(repoMap.Files) != 2 {
		t.Fatalf("repo map files = %d, want 2", len(repoMap.Files))
	}
	stats, err := GetDatabaseStats(db)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SymbolReferenceRows == 0 {
		t.Fatal("expected indexed symbol references")
	}

	related, err := RelatedFiles(db, "pkg/cache.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) == 0 || related[0].Path != "pkg/cache_test.go" {
		t.Fatalf("related files = %+v, want cache_test.go", related)
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
