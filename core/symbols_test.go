package core

import (
	"strings"
	"testing"
)

func TestExtractSymbolReferencesFiltersNoiseAndDefinitions(t *testing.T) {
	content := `package cache

import "testing"

type CacheStore struct{}

func NewCacheStore() CacheStore { return CacheStore{} }

func TestConstructor(t *testing.T) {
	_ = NewCacheStore()
	println("ok")
	len([]int{})
}
`

	refs := ExtractSymbolReferences("go", "pkg/cache_test.go", content)
	names := map[string]bool{}
	for _, ref := range refs {
		names[ref.Name] = true
	}

	if !names["NewCacheStore"] {
		t.Fatalf("expected NewCacheStore reference, got %+v", refs)
	}
	for _, noisy := range []string{"TestConstructor", "println", "len"} {
		if names[noisy] {
			t.Fatalf("unexpected low-signal reference %q in %+v", noisy, refs)
		}
	}
}

func TestExtractGoSymbolsUsesASTForMultilineDeclarations(t *testing.T) {
	content := `package cache

type CacheConfig struct {
	Name string
}

func (store *CacheStore) Load(
	key string,
) (string, error) {
	return lookup(key)
}
`

	symbols := ExtractSymbols("go", "pkg/cache.go", content)
	byName := map[string]Symbol{}
	for _, symbol := range symbols {
		byName[symbol.Name] = symbol
	}

	config := byName["CacheConfig"]
	if config.Kind != "type" || config.Line != 3 || !strings.Contains(config.Signature, "CacheConfig struct") {
		t.Fatalf("unexpected CacheConfig symbol: %+v", config)
	}
	load := byName["Load"]
	if load.Kind != "method" || load.Line != 7 || !strings.Contains(load.Signature, "func (store *CacheStore) Load") {
		t.Fatalf("unexpected Load symbol: %+v", load)
	}
}

func TestExtractPythonSymbolsAddsClassContextForMethods(t *testing.T) {
	content := `class CacheStore:
    def get(self, key):
        return key

    class Nested:
        def load(self):
            return True

def build_cache():
    return CacheStore()
`

	symbols := ExtractSymbols("py", "app/cache.py", content)
	byName := map[string]Symbol{}
	for _, symbol := range symbols {
		byName[symbol.Name] = symbol
	}

	cacheStore := byName["CacheStore"]
	if cacheStore.Kind != "class" || cacheStore.Line != 1 || !strings.Contains(cacheStore.Signature, "class CacheStore") {
		t.Fatalf("unexpected CacheStore symbol: %+v", cacheStore)
	}
	get := byName["get"]
	if get.Kind != "method" || get.Line != 2 || !strings.Contains(get.Signature, "CacheStore.get: def get") {
		t.Fatalf("unexpected get symbol: %+v", get)
	}
	nested := byName["Nested"]
	if nested.Kind != "class" || nested.Line != 5 || !strings.Contains(nested.Signature, "class Nested") {
		t.Fatalf("unexpected Nested symbol: %+v", nested)
	}
	load := byName["load"]
	if load.Kind != "method" || load.Line != 6 || !strings.Contains(load.Signature, "CacheStore.Nested.load: def load") {
		t.Fatalf("unexpected load symbol: %+v", load)
	}
	buildCache := byName["build_cache"]
	if buildCache.Kind != "function" || buildCache.Line != 9 || strings.Contains(buildCache.Signature, "CacheStore.") {
		t.Fatalf("unexpected build_cache symbol: %+v", buildCache)
	}
}

func TestExtractGoSymbolReferencesUsesASTCallsAndCompositeLiterals(t *testing.T) {
	content := `package cache

func build() CacheStore {
	client := providers.NewClient()
	return CacheStore{Client: client}
}
`

	refs := ExtractSymbolReferences("go", "pkg/cache.go", content)
	names := map[string]bool{}
	for _, ref := range refs {
		names[ref.Name] = true
	}

	for _, want := range []string{"NewClient", "CacheStore"} {
		if !names[want] {
			t.Fatalf("missing %s reference in %+v", want, refs)
		}
	}
	if names["build"] {
		t.Fatalf("unexpected self definition reference in %+v", refs)
	}
}

func TestExtractSymbolsDocstrings(t *testing.T) {
	// 1. Go AST comments test
	goContent := `package pkg

// Foo does something cool.
// It is a multiline comment.
func Foo() {}

type (
	// Bar is a type.
	Bar struct{}
)
`
	goSymbols := ExtractSymbols("go", "main.go", goContent)
	goByName := map[string]Symbol{}
	for _, s := range goSymbols {
		goByName[s.Name] = s
	}
	if !strings.Contains(goByName["Foo"].Docstring, "Foo does something cool.") {
		t.Errorf("Foo Docstring missing: %q", goByName["Foo"].Docstring)
	}
	if !strings.Contains(goByName["Bar"].Docstring, "Bar is a type.") {
		t.Errorf("Bar Docstring missing: %q", goByName["Bar"].Docstring)
	}

	// 2. Python lookahead docstring test
	pyContent := `class Calculator:
    """
    Calculator class handles addition.
    """
    def add(a, b):
        """Adds a and b."""
        return a + b
`
	pySymbols := ExtractSymbols("py", "calc.py", pyContent)
	pyByName := map[string]Symbol{}
	for _, s := range pySymbols {
		pyByName[s.Name] = s
	}
	if !strings.Contains(pyByName["Calculator"].Docstring, "Calculator class handles addition.") {
		t.Errorf("Calculator Docstring missing: %q", pyByName["Calculator"].Docstring)
	}
	if pyByName["add"].Docstring != "Adds a and b." {
		t.Errorf("add Docstring = %q, want 'Adds a and b.'", pyByName["add"].Docstring)
	}

	// 3. JS lookbehind docstring test
	jsContent := `/**
 * Connection session validator.
 */
function validateSession() {}

// Single line comment
function helper() {}
`
	jsSymbols := ExtractSymbols("js", "auth.js", jsContent)
	jsByName := map[string]Symbol{}
	for _, s := range jsSymbols {
		jsByName[s.Name] = s
	}
	if !strings.Contains(jsByName["validateSession"].Docstring, "Connection session validator.") {
		t.Errorf("validateSession Docstring missing: %q", jsByName["validateSession"].Docstring)
	}
	if !strings.Contains(jsByName["helper"].Docstring, "Single line comment") {
		t.Errorf("helper Docstring missing: %q", jsByName["helper"].Docstring)
	}
}
