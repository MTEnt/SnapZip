package core

import (
	"strings"
	"testing"
)

func TestBuildContextPackRespectsBudget(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	content := strings.Repeat("class CacheStore\n  def fetch\n    :ok\n  end\nend\n", 80)
	if err := AddKnowledge(db, "rb", "Source file: lib/cache.rb", content); err != nil {
		t.Fatal(err)
	}

	pack, err := BuildContextPack(db, mustTestCompressor(t), "ruby CacheStore fetch", 1, 900, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Snippets) != 1 {
		t.Fatalf("got %d snippets, want 1", len(pack.Snippets))
	}
	if !pack.Truncated {
		t.Fatal("expected context pack to report truncation")
	}
	if pack.UsedBytes > pack.BudgetBytes {
		t.Fatalf("pack used %d bytes, budget %d", pack.UsedBytes, pack.BudgetBytes)
	}
	if pack.Quality.Score <= 0 {
		t.Fatalf("expected context quality score, got %+v", pack.Quality)
	}
	if !strings.Contains(RenderContextPack(pack), "CacheStore") {
		t.Fatal("rendered pack did not include expected content")
	}
	if !strings.Contains(RenderContextPack(pack), "## Context Quality") {
		t.Fatal("rendered pack did not include context quality section")
	}
}

func TestBuildContextPackBoundsLargeFeedback(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "go", "Source file: core/cache.go", "func CacheStore() {}\n"); err != nil {
		t.Fatal(err)
	}
	logged, err := AddFeedback(db, "wrong "+strings.Repeat("feedback ", 300), strings.Repeat("failed output ", 300))
	if err != nil {
		t.Fatal(err)
	}
	if !logged {
		t.Fatal("expected feedback to be logged")
	}

	pack, err := BuildContextPack(db, mustTestCompressor(t), "go CacheStore", 1, 2400, 5)
	if err != nil {
		t.Fatal(err)
	}
	if pack.UsedBytes > pack.BudgetBytes {
		t.Fatalf("pack used %d bytes, budget %d", pack.UsedBytes, pack.BudgetBytes)
	}
	if pack.Quality.Metrics.FeedbackCount == 0 {
		t.Fatalf("context quality did not count feedback: %+v", pack.Quality)
	}
}

func TestBuildContextPackDropsWeakSparseBackfillTail(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "Source file: app/cache.py", "class CacheStore:\n    def fetch(self):\n        return 'hit'\n"); err != nil {
		t.Fatal(err)
	}
	for idx := 0; idx < 4; idx++ {
		content := strings.Repeat("unrelated weather report banana ledger archive\n", 20)
		if err := AddKnowledge(db, "py", "Source file: app/noise_"+string(rune('a'+idx))+".py", content); err != nil {
			t.Fatal(err)
		}
	}

	search, err := SearchMemory(db, mustTestCompressor(t), "CacheStore", 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Snippets) <= 1 {
		t.Fatalf("test setup expected raw search fallback candidates, got %+v", search.Snippets)
	}

	pack, err := BuildContextPack(db, mustTestCompressor(t), "CacheStore", 4, 6000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Snippets) != 1 || pack.Snippets[0].Path != "app/cache.py" {
		t.Fatalf("context pack kept weak fallback snippets:\n%s", RenderContextPack(pack))
	}
}

func TestBuildContextPackAddsResolvedImportNeighbors(t *testing.T) {
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

	pack, err := BuildContextPack(db, mustTestCompressor(t), "CacheStore", 2, 6000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !packHasPath(pack, "app/cache.py") || !packHasPath(pack, "tests/test_cache.py") {
		t.Fatalf("graph-expanded pack missing source or test:\n%s", RenderContextPack(pack))
	}
	if !receiptHasReason(ContextReceipt{Reasons: pack.Receipts[1].Reasons}, "resolved local import graph") {
		t.Fatalf("graph neighbor receipt missing import graph reason: %+v", pack.Receipts)
	}
	if !receiptHasEvidence(pack.Receipts[1], "tests/test_cache.py imports app.cache -> app/cache.py") {
		t.Fatalf("graph neighbor receipt missing resolved edge evidence: %+v", pack.Receipts[1])
	}
}

func TestSearchMemoryAddsResolvedImportNeighbors(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/cache.py", "class CacheStore:\n    pass\n\ndef build_cache():\n    return CacheStore()\n")
	writeTestFile(t, root, "tests/test_cache.py", "from app.cache import build_cache\n\ndef test_build_cache_returns_store():\n    assert build_cache()\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	result, err := SearchMemoryWithMode(db, mustTestCompressor(t), "test_build_cache_returns_store assertion", "test", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !searchResultHasPath(result, "tests/test_cache.py") || !searchResultHasPath(result, "app/cache.py") {
		t.Fatalf("search result missing source/test graph context:\n%s", RenderSearchResult(result))
	}
	if !receiptForPathHasReason(result.Receipts, "app/cache.py", "resolved local import graph") {
		t.Fatalf("search result did not expose graph receipt for source: %+v", result.Receipts)
	}
	rendered := RenderSearchResult(result)
	if !strings.Contains(rendered, "## Context Receipts") ||
		!strings.Contains(rendered, "resolved local import graph") ||
		!strings.Contains(rendered, "app/cache.py") {
		t.Fatalf("rendered search result did not expose graph receipts:\n%s", rendered)
	}
}

func TestSearchMemoryAddsSymbolReferenceDefinition(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/cache.py", "class CacheStore:\n    pass\n\ndef build_cache():\n    return CacheStore()\n")
	writeTestFile(t, root, "tests/test_cache.py", "def test_build_cache_returns_store():\n    assert build_cache()\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	result, err := SearchMemoryWithMode(db, mustTestCompressor(t), "test_build_cache_returns_store assertion", "test", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !searchResultHasPath(result, "tests/test_cache.py") || !searchResultHasPath(result, "app/cache.py") {
		t.Fatalf("search result missing caller/definition symbol graph context:\n%s", RenderSearchResult(result))
	}
	if !receiptForPathHasReason(result.Receipts, "app/cache.py", "local symbol reference graph") {
		t.Fatalf("search result did not expose symbol graph receipt for definition: %+v", result.Receipts)
	}
	if !receiptForPathHasReason(result.Receipts, "app/cache.py", "defines a symbol referenced") {
		t.Fatalf("definition receipt did not explain referenced symbol: %+v", result.Receipts)
	}
}

func TestBuildContextPackAddsSymbolReferenceCaller(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/cache.py", "class CacheStore:\n    pass\n\ndef build_cache():\n    return CacheStore()\n")
	writeTestFile(t, root, "tests/test_cache.py", "def test_build_cache_returns_store():\n    assert build_cache()\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	pack, err := BuildContextPackWithMode(db, mustTestCompressor(t), "CacheStore", "test", 2, 6000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !packHasPath(pack, "app/cache.py") || !packHasPath(pack, "tests/test_cache.py") {
		t.Fatalf("context pack missing definition/caller symbol graph context:\n%s", RenderContextPack(pack))
	}
	if !receiptForPathHasReason(pack.Receipts, "tests/test_cache.py", "local symbol reference graph") {
		t.Fatalf("caller receipt did not explain symbol graph inclusion: %+v", pack.Receipts)
	}
	if pack.Quality.Metrics.GraphReceiptCount == 0 || pack.Quality.Metrics.GraphEvidenceCount == 0 {
		t.Fatalf("context quality did not count structural graph evidence: %+v", pack.Quality.Metrics)
	}
	if !strings.Contains(RenderContextPack(pack), "graph receipts") {
		t.Fatalf("rendered context quality did not expose graph receipt coverage:\n%s", RenderContextPack(pack))
	}
}

func TestBuildContextPackPrunesWeakLexicalTailAfterStrongContext(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/cache.py", strings.Join([]string{
		"class CacheStore:",
		"    def __init__(self, seed):",
		"        self.seed = seed",
		"",
		"    def get(self, key):",
		"        return self.seed if key == 'seed' else None",
		"",
		"def build_cache(seed='ready'):",
		"    return CacheStore(seed)",
	}, "\n"))
	writeTestFile(t, root, "tests/test_cache.py", strings.Join([]string{
		"from app.cache import build_cache",
		"",
		"def test_build_cache_returns_seed():",
		"    cache = build_cache('ready')",
		"    assert cache.get('seed') == 'ready'",
	}, "\n"))
	writeTestFile(t, root, "notes/cache_noise.py", strings.Repeat("cache cache cache archive metadata seed ready\n", 90))

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	pack, err := BuildContextPackWithMode(db, mustTestCompressor(t), "CacheStore build_cache seed test", "test", 4, 8000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !packHasPath(pack, "app/cache.py") || !packHasPath(pack, "tests/test_cache.py") {
		t.Fatalf("context pack missing source/test context:\n%s", RenderContextPack(pack))
	}
	if packHasPath(pack, "notes/cache_noise.py") {
		t.Fatalf("context pack retained weak lexical noise after source/test context:\n%s", RenderContextPack(pack))
	}
	if pack.Quality.Metrics.TestSnippetCount == 0 || pack.Quality.Metrics.DefinitionCount == 0 || pack.Quality.Metrics.ReferenceCount == 0 {
		t.Fatalf("context quality lost expected source/test signals: %+v", pack.Quality)
	}
}

func TestOutgoingGraphContextFocusesImportedSymbolDefinition(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/service.py", strings.Join([]string{
		"from app.providers.stripe import PaymentProvider as BillingGateway",
		"",
		"def checkout():",
		"    return BillingGateway().charge(10)",
	}, "\n"))
	writeTestFile(t, root, "app/providers/stripe.py", strings.Join([]string{
		strings.Repeat("def filler_before():\n    return 'before'\n\n", 12),
		"class PaymentProvider:",
		"    def charge(self, amount):",
		"        return amount",
		"",
		strings.Repeat("def nearby_helper():\n    return 'nearby'\n\n", 8),
		strings.Repeat("def filler_after():\n    return 'far_tail_marker'\n\n", 24),
	}, "\n"))

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	seed, ok, err := indexedSnippetAtLine(db, "app/service.py", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test setup did not index service.py")
	}
	candidates := outgoingGraphContextCandidates(db, seed)
	if len(candidates) == 0 {
		t.Fatal("got no outgoing graph candidates")
	}
	if candidates[0].snippet.Path != "app/providers/stripe.py" {
		t.Fatalf("top outgoing candidate path = %q, want provider: %+v", candidates[0].snippet.Path, candidates)
	}
	if !strings.Contains(candidates[0].snippet.Content, "class PaymentProvider") {
		t.Fatalf("focused provider snippet missing imported class:\n%s", candidates[0].snippet.Content)
	}
	if strings.Contains(candidates[0].snippet.Content, "far_tail_marker") {
		t.Fatalf("focused provider snippet retained distant tail content:\n%s", candidates[0].snippet.Content)
	}
	if !receiptHasReason(candidates[0].receipt, "focused on imported symbol definition") {
		t.Fatalf("receipt did not explain imported-symbol focus: %+v", candidates[0].receipt)
	}
	if !receiptHasEvidence(candidates[0].receipt, "imported symbol PaymentProvider") {
		t.Fatalf("receipt missing imported symbol evidence: %+v", candidates[0].receipt)
	}
}

func TestBuildRepairContextPackPrefersSourceSymbolExcerpt(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	source := strings.Join([]string{
		"def unrelated_live_filter(info):",
		"    return info.get('is_live')",
		"",
		strings.Repeat("def filler():\n    return 'is_live'\n\n", 20),
		"def match_str(filter_str, dct):",
		"    if filter_str == 'is_live':",
		"        return dct.get('is_live') is not None",
		"    return False",
	}, "\n")
	testSource := strings.Join([]string{
		"def test_match_str():",
		"    assert not match_str('is_live', {'is_live': False})",
	}, "\n")
	unrelated := strings.Repeat("is_live extractor archived stream metadata\n", 80)

	if _, err := AddKnowledgeContent(db, "py", "Source file: youtube_dl/utils.py", "youtube_dl/utils.py", []byte(source), 512, 0); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSymbolsForFile(db, "py", "youtube_dl/utils.py", []byte(source)); err != nil {
		t.Fatal(err)
	}
	if _, err := AddKnowledgeContent(db, "py", "Source file: test/test_utils.py", "test/test_utils.py", []byte(testSource), 512, 0); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSymbolsForFile(db, "py", "test/test_utils.py", []byte(testSource)); err != nil {
		t.Fatal(err)
	}
	if _, err := AddKnowledgeContent(db, "py", "Source file: youtube_dl/extractor/bambuser.py", "youtube_dl/extractor/bambuser.py", []byte(unrelated), 512, 0); err != nil {
		t.Fatal(err)
	}

	failure := strings.Join([]string{
		"Traceback (most recent call last):",
		`  File "/tmp/work/test/test_utils.py", line 2, in test_match_str`,
		"    assert not match_str('is_live', {'is_live': False})",
		"AssertionError: True is not false",
	}, "\n")
	pack, err := BuildRepairContextPack(db, mustTestCompressor(t), failure, "", "debug", 4, 4096, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Snippets) == 0 {
		t.Fatal("got no repair snippets")
	}
	if len(pack.Receipts) == 0 {
		t.Fatalf("repair pack did not include context receipts:\n%s", RenderContextPack(pack))
	}
	if pack.Snippets[0].Path != "youtube_dl/utils.py" {
		t.Fatalf("top repair snippet = %q, want source function:\n%s", pack.Snippets[0].Path, RenderContextPack(pack))
	}
	if pack.Receipts[0].Path != "youtube_dl/utils.py" || !receiptHasReason(pack.Receipts[0], "failure-related symbol") {
		t.Fatalf("top receipt did not explain source-symbol match: %+v", pack.Receipts[0])
	}
	if !strings.Contains(pack.Snippets[0].Content, "def match_str") {
		t.Fatalf("top repair snippet did not include focused function:\n%s", pack.Snippets[0].Content)
	}
	if strings.Contains(pack.Snippets[0].Path, "bambuser") {
		t.Fatalf("unrelated extractor ranked first:\n%s", RenderContextPack(pack))
	}
	if pack.Quality.Metrics.DefinitionCount == 0 || pack.Quality.Metrics.TestSnippetCount == 0 {
		t.Fatalf("repair pack quality missed definition/test coverage: %+v", pack.Quality)
	}
	if !receiptHasReason(ContextReceipt{Reasons: pack.Quality.Strengths}, "test context") {
		t.Fatalf("repair pack quality did not report test context: %+v", pack.Quality)
	}
}

func receiptHasReason(receipt ContextReceipt, want string) bool {
	for _, reason := range receipt.Reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}

func receiptHasEvidence(receipt ContextReceipt, want string) bool {
	for _, evidence := range receipt.Evidence {
		if strings.Contains(evidence, want) {
			return true
		}
	}
	return false
}

func packHasPath(pack ContextPack, path string) bool {
	for _, snippet := range pack.Snippets {
		if snippet.Path == path {
			return true
		}
	}
	return false
}

func searchResultHasPath(result SearchResult, path string) bool {
	for _, snippet := range result.Snippets {
		if snippet.Path == path {
			return true
		}
	}
	return false
}

func receiptForPathHasReason(receipts []ContextReceipt, path, want string) bool {
	for _, receipt := range receipts {
		if receipt.Path == path && receiptHasReason(receipt, want) {
			return true
		}
	}
	return false
}

func TestExpandQueryForPackMode(t *testing.T) {
	if got := ExpandQueryForPackMode("cache", "debug"); !strings.Contains(got, "failure") {
		t.Fatalf("debug mode did not add failure terms: %q", got)
	}
	if got := ExpandQueryForPackMode("cache", "refactor"); !strings.Contains(got, "caller") {
		t.Fatalf("refactor mode did not add caller terms: %q", got)
	}
	if got := ExpandQueryForPackMode("cache", "test"); !strings.Contains(got, "assertion") {
		t.Fatalf("test mode did not add assertion terms: %q", got)
	}
	if got := ExpandQueryForPackMode("cache", "docs"); !strings.Contains(got, "documentation") {
		t.Fatalf("docs mode did not add documentation terms: %q", got)
	}
	if got := ExpandQueryForPackMode("cache", "review"); !strings.Contains(got, "regression") {
		t.Fatalf("review mode did not add review terms: %q", got)
	}

	codeQuery := "async def checkout(self):\n    return self.resolve_payment_gateway(order_id)"
	expanded := ExpandQueryForPackMode(codeQuery, "debug")
	if !strings.Contains(expanded, codeQuery) {
		t.Fatalf("expanded query did not preserve original query: %q", expanded)
	}
	if !strings.Contains(expanded, "checkout resolve_payment_gateway") {
		t.Fatalf("expanded query did not include planned code identifiers: %q", expanded)
	}
	if !strings.Contains(expanded, "stack trace") {
		t.Fatalf("expanded query missing debug mode terms: %q", expanded)
	}
}

func TestBuildContextPackAddsDependentTypeContext(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/config.py", `class CacheConfig:
    def __init__(self, ttl):
        self.ttl = ttl
`)
	writeTestFile(t, root, "app/cache.py", `from app.config import CacheConfig

class BoundedCache:
    def __init__(self, config: CacheConfig):
        self.config = config

    def evict_keys(self):
        return self.config.ttl
`)

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	pack, err := BuildContextPackWithMode(db, mustTestCompressor(t), "evict_keys", "", 3, 12000, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Verify both evict_keys method and CacheConfig class definition are retrieved
	if !packHasPath(pack, "app/cache.py") || !packHasPath(pack, "app/config.py") {
		t.Fatalf("context pack missing source or config file: \n%s", RenderContextPack(pack))
	}

	// Make sure the receipt explains dependent type inclusion
	if !receiptForPathHasReason(pack.Receipts, "app/config.py", "dependent type expansion") {
		t.Fatalf("receipts missing dependent type expansion explanation: %+v", pack.Receipts)
	}
}
