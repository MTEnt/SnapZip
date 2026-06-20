package core

import "testing"

func TestExtractSymbolReferencesFiltersNoiseAndDefinitions(t *testing.T) {
	content := `package cache

type CacheStore struct{}

func NewCacheStore() CacheStore { return CacheStore{} }

func TestConstructor() {
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
