package core

import (
	"testing"

	"github.com/klauspost/compress/zstd"
)

func mustTestCompressor(t *testing.T) *ZstdCompressor {
	t.Helper()
	comp, err := NewZstdCompressor(zstd.SpeedFastest)
	if err != nil {
		t.Fatal(err)
	}
	return comp
}

func TestDetectLanguageUsesExactAliases(t *testing.T) {
	cases := map[string]string{
		"python lru cache":       "py",
		"javascript react hook":  "js",
		"typescript type guard":  "ts",
		"rust ownership helper":  "rs",
		"sqlite migration query": "sql",
		"algorithm search":       "",
	}

	for input, want := range cases {
		if got := DetectLanguage(input); got != want {
			t.Fatalf("DetectLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLanguageFilterAcceptsNamesAndExtensions(t *testing.T) {
	filter := NewLanguageFilter("python, javascript, rust")
	for _, lang := range []string{"py", "python", "js", "jsx", "rs", "rust"} {
		if !filter.Matches(lang) {
			t.Fatalf("expected filter to match %q", lang)
		}
	}
	if filter.Matches("go") {
		t.Fatal("did not expect filter to match go")
	}

	custom := NewLanguageFilter("bf")
	if !custom.Matches("bf") {
		t.Fatal("expected explicit custom extension bf to match")
	}
}

func TestRetrieveSimilarSnippetsMatchesPythonAlias(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "LRU cache", "from functools import lru_cache\n"); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "python lru cache", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Language != "py" {
		t.Fatalf("got language %q, want py", results[0].Language)
	}
}
