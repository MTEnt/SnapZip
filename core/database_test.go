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
		"c# service":             "cs",
		"c++ parser":             "cpp",
		"css grid layout":        "css",
		"html form":              "html",
		"javascript react hook":  "js",
		"python lru cache":       "py",
		"ruby rake task":         "rb",
		"rust ownership helper":  "rs",
		"sqlite migration query": "sql",
		"typescript type guard":  "ts",
		"vue component":          "vue",
		"algorithm search":       "",
	}

	for input, want := range cases {
		if got := DetectLanguage(input); got != want {
			t.Fatalf("DetectLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLanguageFilterAcceptsNamesAndExtensions(t *testing.T) {
	filter := NewLanguageFilter("python, javascript, rust, ruby, html, css")
	for _, lang := range []string{"css", "html", "py", "python", "js", "rs", "rust", "rb", "ruby"} {
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

func TestLanguageFilterGroups(t *testing.T) {
	popular := NewLanguageFilter("popular")
	for _, lang := range []string{"css", "html", "java", "js", "php", "py", "rb", "rs", "sql", "ts"} {
		if !popular.Matches(lang) {
			t.Fatalf("expected popular preset to match %q", lang)
		}
	}

	web := NewLanguageFilter("web")
	for _, lang := range []string{"astro", "css", "html", "jsx", "scss", "svelte", "tsx", "vue"} {
		if !web.Matches(lang) {
			t.Fatalf("expected web preset to match %q", lang)
		}
	}
	if web.Matches("rb") {
		t.Fatal("did not expect web preset to match ruby")
	}
}

func TestLanguageFromPathHandlesPopularFiles(t *testing.T) {
	cases := map[string]string{
		"app/views/index.html":  "html",
		"assets/site.css":       "css",
		"components/Button.tsx": "tsx",
		"Gemfile":               "rb",
		"Rakefile":              "rb",
		"Dockerfile":            "dockerfile",
		"CMakeLists.txt":        "cmake",
		"WORKSPACE.bazel":       "starlark",
		"terraform/main.tfvars": "tfvars",
		"config/settings.yml":   "yaml",
		"scripts/deploy.bash":   "sh",
		"server.cjs":            "cjs",
	}

	for path, want := range cases {
		if got := LanguageFromPath(path); got != want {
			t.Fatalf("LanguageFromPath(%q) = %q, want %q", path, got, want)
		}
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
