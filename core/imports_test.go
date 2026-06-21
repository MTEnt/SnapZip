package core

import "testing"

func TestExtractGoImportsUsesASTForBlocksAndAliases(t *testing.T) {
	content := `package cache

import (
	json "encoding/json"
	. "fmt"
	_ "net/http/pprof"
	"app/internal/cache"
)
`

	imports := ExtractImports("go", "pkg/cache.go", content)
	byPath := map[string]ImportReference{}
	for _, ref := range imports {
		byPath[ref.ImportPath] = ref
	}

	cases := map[string]struct {
		alias string
		line  int
	}{
		"encoding/json":      {"json", 4},
		"fmt":                {".", 5},
		"net/http/pprof":     {"_", 6},
		"app/internal/cache": {"", 7},
	}
	for path, want := range cases {
		got := byPath[path]
		if got.ImportPath == "" {
			t.Fatalf("missing import %q in %+v", path, imports)
		}
		if got.Alias != want.alias || got.Line != want.line || got.Context == "" {
			t.Fatalf("import %q = %+v, want alias %q line %d with context", path, got, want.alias, want.line)
		}
	}
}
