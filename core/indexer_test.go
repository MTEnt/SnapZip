package core

import (
	"database/sql"
	"fmt"
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

func TestSplitContentChunksForLanguageUsesCodeBoundaries(t *testing.T) {
	header := "import os\n\n"
	alpha := "def alpha():\n    alpha_value = 'a'\n    return alpha_value\n\n"
	beta := "def beta():\n    beta_value = 'b'\n    return beta_value\n"
	content := []byte(header + alpha + beta)
	maxBytes := len([]byte(header+alpha)) + 1

	chunks := splitContentChunksForLanguage("py", content, maxBytes)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2: %+v", len(chunks), chunks)
	}
	if strings.Contains(string(chunks[0].Data), "def beta") {
		t.Fatalf("first chunk crossed into beta function:\n%s", string(chunks[0].Data))
	}
	if !strings.Contains(string(chunks[1].Data), "def beta") {
		t.Fatalf("second chunk missing beta function:\n%s", string(chunks[1].Data))
	}
	if chunks[1].StartLine <= chunks[0].EndLine {
		t.Fatalf("chunk line ranges overlap: first %d-%d second %d-%d", chunks[0].StartLine, chunks[0].EndLine, chunks[1].StartLine, chunks[1].EndLine)
	}
}

func TestSplitContentChunksForLanguageUsesSymbolsAcrossLanguages(t *testing.T) {
	cases := []struct {
		name       string
		language   string
		secondName string
		content    string
	}{
		{
			name:       "go functions",
			language:   "go",
			secondName: "func Beta",
			content:    "package pkg\n\nfunc Alpha() string {\n\treturn \"a\"\n}\n\nfunc Beta() string {\n\treturn \"b\"\n}\n",
		},
		{
			name:       "typescript interfaces",
			language:   "ts",
			secondName: "interface Beta",
			content:    "export interface Alpha {\n  id: string\n}\n\nexport interface Beta {\n  id: string\n}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			maxBytes := strings.Index(tc.content, tc.secondName)
			if maxBytes <= 0 {
				t.Fatalf("test setup did not find second boundary %q", tc.secondName)
			}
			chunks := splitContentChunksForLanguage(tc.language, []byte(tc.content), maxBytes+1)
			if len(chunks) != 2 {
				t.Fatalf("got %d chunks, want 2: %+v", len(chunks), chunks)
			}
			if strings.Contains(string(chunks[0].Data), tc.secondName) {
				t.Fatalf("first chunk crossed into second block:\n%s", string(chunks[0].Data))
			}
			if !strings.Contains(string(chunks[1].Data), tc.secondName) {
				t.Fatalf("second chunk missing second block:\n%s", string(chunks[1].Data))
			}
		})
	}
}

func TestSplitContentChunksForGoUsesASTDeclarationSpans(t *testing.T) {
	registry := "var HugeRegistry = map[string]string{\n" +
		strings.Repeat("\t\"feature\": \"enabled\",\n", 18) +
		"}\n\n"
	alpha := "func Alpha() string {\n\treturn \"alpha\"\n}\n"
	beta := "func Beta() string {\n" +
		strings.Repeat("\tvalue := \"beta\"\n", 5) +
		"\treturn value\n}\n"
	content := "package pkg\n\n" + registry + alpha + "\n" + beta
	maxBytes := len([]byte("\n"+alpha)) + 2

	chunks := splitContentChunksForLanguage("go", []byte(content), maxBytes)
	var alphaChunk contentChunk
	for _, chunk := range chunks {
		if strings.Contains(string(chunk.Data), "func Alpha") {
			alphaChunk = chunk
			break
		}
	}
	if len(alphaChunk.Data) == 0 {
		t.Fatalf("no chunk contained Alpha: %+v", chunks)
	}
	alphaContent := string(alphaChunk.Data)
	if !strings.Contains(alphaContent, "return \"alpha\"") {
		t.Fatalf("Alpha chunk is incomplete:\n%s", alphaContent)
	}
	if strings.Contains(alphaContent, "HugeRegistry") {
		t.Fatalf("Alpha chunk retained unrelated top-level var:\n%s", alphaContent)
	}
	if strings.Contains(alphaContent, "func Beta") {
		t.Fatalf("Alpha chunk crossed into sibling function:\n%s", alphaContent)
	}
}

func TestSplitContentChunksForPythonUsesTopLevelStructuralSpans(t *testing.T) {
	routes := "ROUTES = {\n" +
		strings.Repeat("    \"feature\": \"enabled\",\n", 18) +
		"}\n\n"
	alpha := "def alpha():\n    return \"alpha\"\n"
	beta := "def beta():\n" +
		strings.Repeat("    value = \"beta\"\n", 5) +
		"    return value\n"
	content := routes + alpha + "\n" + beta
	maxBytes := len([]byte("\n"+alpha)) + 2

	chunks := splitContentChunksForLanguage("py", []byte(content), maxBytes)
	var alphaChunk contentChunk
	for _, chunk := range chunks {
		if strings.Contains(string(chunk.Data), "def alpha") {
			alphaChunk = chunk
			break
		}
	}
	if len(alphaChunk.Data) == 0 {
		t.Fatalf("no chunk contained alpha: %+v", chunks)
	}
	alphaContent := string(alphaChunk.Data)
	if !strings.Contains(alphaContent, "return \"alpha\"") {
		t.Fatalf("alpha chunk is incomplete:\n%s", alphaContent)
	}
	if strings.Contains(alphaContent, "ROUTES") {
		t.Fatalf("alpha chunk retained unrelated top-level data:\n%s", alphaContent)
	}
	if strings.Contains(alphaContent, "def beta") {
		t.Fatalf("alpha chunk crossed into sibling function:\n%s", alphaContent)
	}
}

func TestSplitContentChunksForPopularLanguagesUsesTopLevelStructuralSpans(t *testing.T) {
	cases := []struct {
		name      string
		language  string
		content   string
		target    string
		required  string
		forbidden []string
	}{
		{
			name:     "javascript declarations",
			language: "js",
			content: "const registry = {\n" +
				strings.Repeat("  feature: \"enabled\",\n", 18) +
				"};\n\n" +
				"export function alpha() {\n  return \"alpha\"\n}\n\n" +
				"export function beta() {\n" +
				strings.Repeat("  const value = \"beta\"\n", 5) +
				"  return value\n}\n",
			target:    "function alpha",
			required:  "return \"alpha\"",
			forbidden: []string{"const registry", "function beta"},
		},
		{
			name:     "typescript interfaces",
			language: "ts",
			content: "export interface Alpha {\n  id: string\n  name: string\n}\n\n" +
				"export interface Beta {\n" +
				strings.Repeat("  value: string\n", 5) +
				"}\n",
			target:    "interface Alpha",
			required:  "name: string",
			forbidden: []string{"interface Beta"},
		},
		{
			name:     "java classes",
			language: "java",
			content: "public class Alpha {\n  String value() {\n    return \"alpha\";\n  }\n}\n\n" +
				"public class Beta {\n" +
				strings.Repeat("  String value = \"beta\";\n", 5) +
				"}\n",
			target:    "class Alpha",
			required:  "return \"alpha\"",
			forbidden: []string{"class Beta"},
		},
		{
			name:     "rust declarations",
			language: "rs",
			content: "pub static REGISTRY: &[&str] = &[\n" +
				strings.Repeat("    \"feature\",\n", 18) +
				"];\n\n" +
				"pub fn alpha() -> &'static str {\n    \"alpha\"\n}\n\n" +
				"pub fn beta() -> String {\n" +
				strings.Repeat("    let value = \"beta\";\n", 5) +
				"    value.to_string()\n}\n",
			target:    "fn alpha",
			required:  "\"alpha\"",
			forbidden: []string{"REGISTRY", "fn beta"},
		},
		{
			name:     "ruby classes",
			language: "rb",
			content: "class Alpha\n  def value\n    \"alpha\"\n  end\nend\n\n" +
				"class Beta\n" +
				strings.Repeat("  def value\n    \"beta\"\n  end\n", 5) +
				"end\n",
			target:    "class Alpha",
			required:  "\"alpha\"",
			forbidden: []string{"class Beta"},
		},
		{
			name:     "cpp functions",
			language: "cpp",
			content: "static const char* registry[] = {\n" +
				strings.Repeat("  \"feature\",\n", 18) +
				"};\n\n" +
				"std::string alpha() {\n  return \"alpha\";\n}\n\n" +
				"std::string beta() {\n" +
				strings.Repeat("  auto value = \"beta\";\n", 5) +
				"  return value;\n}\n",
			target:    "alpha()",
			required:  "return \"alpha\"",
			forbidden: []string{"registry", "beta()"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			maxBytes := len([]byte(tc.required)) + 80
			chunks := splitContentChunksForLanguage(tc.language, []byte(tc.content), maxBytes)
			assertFocusedChunk(t, chunks, tc.target, tc.required, tc.forbidden...)
		})
	}
}

func assertFocusedChunk(t *testing.T, chunks []contentChunk, target, required string, forbidden ...string) {
	t.Helper()
	var found contentChunk
	for _, chunk := range chunks {
		if strings.Contains(string(chunk.Data), target) {
			found = chunk
			break
		}
	}
	if len(found.Data) == 0 {
		t.Fatalf("no chunk contained %q: %+v", target, chunks)
	}
	content := string(found.Data)
	if !strings.Contains(content, required) {
		t.Fatalf("focused chunk for %q missing %q:\n%s", target, required, content)
	}
	for _, unexpected := range forbidden {
		if strings.Contains(content, unexpected) {
			t.Fatalf("focused chunk for %q retained unrelated %q:\n%s", target, unexpected, content)
		}
	}
}

func TestFindAffectedTestsUsesDirectAndRelatedSignals(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pkg/cache.go", "package cache\n\ntype CacheStore struct{}\n\nfunc NewCacheStore() CacheStore { return CacheStore{} }\n")
	writeTestFile(t, root, "pkg/cache_test.go", "package cache\n\nimport \"testing\"\n\nfunc TestConstructor(t *testing.T) { _ = NewCacheStore() }\n")

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
	writeTestFile(t, root, "pkg/cache_test.go", "package cache\n\nimport \"testing\"\n\nfunc TestConstructor(t *testing.T) { _ = NewCacheStore() }\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	options := DefaultIndexOptions()
	options.KnowledgeCards = true
	count, err := IndexDirectoryWithOptions(db, root, NewLanguageFilter("go"), options)
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
	if stats.ImportRows == 0 {
		t.Fatal("expected indexed import references")
	}
	if stats.KnowledgeCardRows == 0 {
		t.Fatal("expected indexed knowledge cards")
	}
	assertMetadataFTSRowCounts(t, db)

	related, err := RelatedFiles(db, "pkg/cache.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) == 0 || related[0].Path != "pkg/cache_test.go" {
		t.Fatalf("related files = %+v, want cache_test.go", related)
	}

	plan, err := BuildValidationPlan(db, []string{"pkg/cache.go"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.SuggestedCommands) == 0 || !strings.Contains(plan.SuggestedCommands[0].Command, "go test ./pkg") {
		t.Fatalf("validation plan missing Go test command: %+v", plan.SuggestedCommands)
	}
}

func TestSearchSymbolsKeepsNameMatchWhenFTSCandidatesAreCrowded(t *testing.T) {
	root := t.TempDir()
	for idx := 0; idx < metadataFTSCandidateLimit(1)+20; idx++ {
		writeTestFile(t, root, fmt.Sprintf("pkg/decoy_%03d.go", idx), fmt.Sprintf("package pkg\n\nfunc Decoy%03d() { _ = CacheStore{} }\n", idx))
	}
	writeTestFile(t, root, "pkg/cache.go", "package pkg\n\ntype CacheStore struct{}\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("go")); err != nil {
		t.Fatal(err)
	}

	symbols, err := SearchSymbols(db, "CacheStore", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 {
		t.Fatalf("got %d symbols, want 1", len(symbols))
	}
	if symbols[0].Name != "CacheStore" || symbols[0].Path != "pkg/cache.go" {
		t.Fatalf("top crowded symbol = %+v, want CacheStore from pkg/cache.go", symbols[0])
	}
}

func TestImportsConnectSourceAndTests(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/cache.py", "class CacheStore:\n    pass\n\ndef build_cache():\n    return CacheStore()\n")
	writeTestFile(t, root, "tests/test_cache.py", "from app.cache import build_cache\n\ndef test_build_cache():\n    assert build_cache()\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	imports, err := SearchImports(db, "app.cache", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) == 0 || imports[0].Path != "tests/test_cache.py" {
		t.Fatalf("imports = %+v, want tests/test_cache.py", imports)
	}
	if imports[0].TargetPath != "app/cache.py" {
		t.Fatalf("resolved import target = %q, want app/cache.py", imports[0].TargetPath)
	}

	related, err := RelatedFiles(db, "app/cache.py", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) == 0 || related[0].Path != "tests/test_cache.py" {
		t.Fatalf("related files = %+v, want tests/test_cache.py", related)
	}

	reverseRelated, err := RelatedFiles(db, "tests/test_cache.py", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reverseRelated) == 0 || reverseRelated[0].Path != "app/cache.py" {
		t.Fatalf("reverse related files = %+v, want app/cache.py", reverseRelated)
	}

	graph, err := BuildDependencyGraph(db, "app/cache.py", 10)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Path != "app/cache.py" || graph.Language != "py" {
		t.Fatalf("graph identity = %+v, want app/cache.py [py]", graph)
	}
	if len(graph.ImportedBy) == 0 || graph.ImportedBy[0].Path != "tests/test_cache.py" {
		t.Fatalf("graph imported_by = %+v, want tests/test_cache.py", graph.ImportedBy)
	}
	if len(graph.Symbols) == 0 {
		t.Fatalf("graph symbols empty: %+v", graph)
	}
	if len(graph.ReferencedBy) == 0 || graph.ReferencedBy[0].Path != "tests/test_cache.py" {
		t.Fatalf("graph referenced_by = %+v, want tests/test_cache.py", graph.ReferencedBy)
	}
	renderedGraph := RenderDependencyGraph(graph)
	if !strings.Contains(renderedGraph, "Imported By") || !strings.Contains(renderedGraph, "Referenced By") {
		t.Fatalf("rendered graph missing import or symbol graph section:\n%s", renderedGraph)
	}
	assertMetadataFTSRowCounts(t, db)

	testGraph, err := BuildDependencyGraph(db, "tests/test_cache.py", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(testGraph.Imports) == 0 || testGraph.Imports[0].TargetPath != "app/cache.py" {
		t.Fatalf("test graph imports = %+v, want resolved app/cache.py", testGraph.Imports)
	}
	if len(testGraph.ReferenceDefinitions) == 0 || testGraph.ReferenceDefinitions[0].Path != "app/cache.py" {
		t.Fatalf("test graph reference definitions = %+v, want app/cache.py", testGraph.ReferenceDefinitions)
	}

	pack, err := BuildContextPack(db, mustTestCompressor(t), "build_cache", 5, 4096, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !receiptEvidenceContains(pack.Receipts, "imports app.cache") {
		t.Fatalf("context receipts missing resolved import evidence: %+v", pack.Receipts)
	}
}

func TestResolvedImportsHandleRelativeTypeScript(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "src/cache.ts", "export function buildCache() { return new Map<string, string>() }\n")
	writeTestFile(t, root, "tests/cache.test.ts", "import { buildCache } from \"../src/cache\"\n\ntest(\"cache\", () => buildCache())\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("typescript")); err != nil {
		t.Fatal(err)
	}

	imports, err := SearchImports(db, "../src/cache", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) == 0 || imports[0].TargetPath != "src/cache.ts" {
		t.Fatalf("imports = %+v, want resolved src/cache.ts", imports)
	}
	targetImports, err := SearchImports(db, "src/cache.ts", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(targetImports) == 0 || targetImports[0].Path != "tests/cache.test.ts" {
		t.Fatalf("target path import lookup = %+v, want tests/cache.test.ts", targetImports)
	}
	related, err := RelatedFiles(db, "src/cache.ts", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) == 0 || related[0].Path != "tests/cache.test.ts" {
		t.Fatalf("related files = %+v, want tests/cache.test.ts", related)
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

func receiptEvidenceContains(receipts []ContextReceipt, needle string) bool {
	for _, receipt := range receipts {
		for _, evidence := range receipt.Evidence {
			if strings.Contains(evidence, needle) {
				return true
			}
		}
	}
	return false
}

func assertMetadataFTSRowCounts(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, pair := range []struct {
		source string
		fts    string
	}{
		{"symbols", "symbols_fts"},
		{"symbol_refs", "symbol_refs_fts"},
		{"import_refs", "import_refs_fts"},
	} {
		sourceCount, err := tableRowCount(db, pair.source)
		if err != nil {
			t.Fatal(err)
		}
		ftsCount, err := tableRowCount(db, pair.fts)
		if err != nil {
			t.Fatal(err)
		}
		if sourceCount != ftsCount {
			t.Fatalf("%s has %d rows, %s has %d rows", pair.source, sourceCount, pair.fts, ftsCount)
		}
	}
}

func TestSplitContentChunksWithOverlap(t *testing.T) {
	content := []byte("line1\nline2\nline3\nline4\nline5\n")
	chunks := splitContentChunks(content, 12)

	if len(chunks) != 4 {
		t.Fatalf("got %d chunks, want 4", len(chunks))
	}

	// Chunk 1: "line1\nline2\n"
	if chunks[0].StartLine != 1 || chunks[0].EndLine != 2 {
		t.Errorf("chunk 0 lines: got %d-%d, want 1-2", chunks[0].StartLine, chunks[0].EndLine)
	}
	if string(chunks[0].Data) != "line1\nline2\n" {
		t.Errorf("chunk 0 data: got %q, want %q", string(chunks[0].Data), "line1\nline2\n")
	}

	// Chunk 2: "2\nline3\n"
	if chunks[1].StartLine != 2 || chunks[1].EndLine != 3 {
		t.Errorf("chunk 1 lines: got %d-%d, want 2-3", chunks[1].StartLine, chunks[1].EndLine)
	}
	if string(chunks[1].Data) != "2\nline3\n" {
		t.Errorf("chunk 1 data: got %q, want %q", string(chunks[1].Data), "2\nline3\n")
	}

	// Chunk 3: "3\nline4\n"
	if chunks[2].StartLine != 3 || chunks[2].EndLine != 4 {
		t.Errorf("chunk 2 lines: got %d-%d, want 3-4", chunks[2].StartLine, chunks[2].EndLine)
	}
	if string(chunks[2].Data) != "3\nline4\n" {
		t.Errorf("chunk 2 data: got %q, want %q", string(chunks[2].Data), "3\nline4\n")
	}

	// Chunk 4: "4\nline5\n"
	if chunks[3].StartLine != 4 || chunks[3].EndLine != 5 {
		t.Errorf("chunk 3 lines: got %d-%d, want 4-5", chunks[3].StartLine, chunks[3].EndLine)
	}
	if string(chunks[3].Data) != "4\nline5\n" {
		t.Errorf("chunk 3 data: got %q, want %q", string(chunks[3].Data), "4\nline5\n")
	}
}
