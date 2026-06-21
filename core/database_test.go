package core

import (
	"fmt"
	"strings"
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

func TestSearchTokensExpandsCodeIdentifiers(t *testing.T) {
	tokens := searchTokens("CacheStore HTTPServer cache_store parseURL v2Client")
	for _, want := range []string{
		"cachestore",
		"cache",
		"store",
		"httpserver",
		"http",
		"server",
		"cache_store",
		"parseurl",
		"parse",
		"url",
		"v2client",
		"client",
	} {
		if !stringSliceContains(tokens, want) {
			t.Fatalf("searchTokens missing %q in %+v", want, tokens)
		}
	}
}

func TestPlanRetrievalQueryFiltersLowSignalCodeTokens(t *testing.T) {
	plan := planRetrievalQuery("async def checkout(self):\n    return self.resolve_payment_gateway(order_id)")

	for _, want := range []string{"checkout", "resolve_payment_gateway", "resolve", "payment", "gateway", "order_id", "order"} {
		if !stringSliceContains(plan.RankingTokens, want) {
			t.Fatalf("planned ranking tokens missing %q: %+v", want, plan.RankingTokens)
		}
	}
	for _, unwanted := range []string{"async", "def", "self", "return"} {
		if stringSliceContains(plan.FTSTokens, unwanted) {
			t.Fatalf("planned FTS tokens included low-signal token %q: %+v", unwanted, plan.FTSTokens)
		}
	}
	if plan.StructuredPrompt == "" || strings.Contains(plan.StructuredPrompt, " return ") {
		t.Fatalf("structured prompt was not compacted: %q", plan.StructuredPrompt)
	}
}

func TestPlanRetrievalQueryKeepsNaturalLanguageTokens(t *testing.T) {
	plan := planRetrievalQuery("go install setup readme")

	for _, want := range []string{"go", "install", "setup", "readme"} {
		if !stringSliceContains(plan.FTSTokens, want) {
			t.Fatalf("natural query token %q missing from FTS tokens: %+v", want, plan.FTSTokens)
		}
	}
}

func TestPlanRetrievalQueryAddsExpandedIdentifierFTSPath(t *testing.T) {
	plan := planRetrievalQuery("fix getOrCreate refreshToken")

	if !tokenPathContainsAll(plan.FTSPaths, "get", "create", "refresh", "token") {
		t.Fatalf("planned FTS paths did not include expanded identifier path: %+v", plan.FTSPaths)
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

func TestInitDBCreatesSearchIndexes(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, name := range []string{
		"knowledge_path_idx",
		"symbols_name_idx",
		"symbol_refs_name_idx",
		"import_refs_import_path_idx",
		"import_refs_target_path_idx",
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", name, count)
		}
	}

	for _, name := range []string{"symbols_fts", "symbol_refs_fts", "import_refs_fts"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("metadata FTS table %s count = %d, want 1", name, count)
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

func TestRetrieveSimilarSnippetsDoesNotHardFilterDetectedLanguage(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "go", "Source file: cmd/snapzip/main.go", "go command routing and cli flags\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "md", "Source file: README.md", "Install SnapZip with go install and run setup commands\n"); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "go install setup readme", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("got no results")
	}
	if results[0].Topic != "Source file: README.md" {
		t.Fatalf("top result = %q, want README.md", results[0].Topic)
	}
}

func TestRetrieveSimilarSnippetsSanitizesUnderscoreQueries(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	content := "skip node_modules, binary files, generated output, and oversized source files during indexing\n"
	if err := AddKnowledge(db, "go", "Source file: core/indexer.go", content); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "skip node_modules binary files indexer", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("got no results")
	}
	if results[0].Topic != "Source file: core/indexer.go" {
		t.Fatalf("top result = %q, want indexer.go", results[0].Topic)
	}
}

func TestRetrieveSimilarSnippetsUsesLexicalRelevance(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "go", "Source file: cmd/snapzip/main.go", "feedback command handler and search command wiring\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "go", "Source file: core/feedback_test.go", "negative feedback sentiment detection test cases for incorrect broken generated code\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "go", "Source file: core/feedback.go", "negative feedback sentiment detection for incorrect broken generated code\n"); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "negative feedback sentiment error handling", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("got no results")
	}
	if results[0].Topic != "Source file: core/feedback.go" {
		t.Fatalf("top result = %q, want feedback.go", results[0].Topic)
	}
}

func TestRetrieveSimilarSnippetsBackfillsSparseFTSMatches(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "Source file: target.py", "def rare_unique_token():\n    return True\n"); err != nil {
		t.Fatal(err)
	}
	for idx := 0; idx < 5; idx++ {
		if err := AddKnowledge(db, "py", fmt.Sprintf("Source file: fallback_%d.py", idx), fmt.Sprintf("def fallback_%d():\n    return %d\n", idx, idx)); err != nil {
			t.Fatal(err)
		}
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "rare_unique_token", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("got %d result(s), want 5 with fallback candidates: %+v", len(results), results)
	}
	if results[0].Topic != "Source file: target.py" {
		t.Fatalf("top result = %q, want target.py", results[0].Topic)
	}

	limited, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "rare_unique_token", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit=1 returned %d result(s), want 1: %+v", len(limited), limited)
	}
}

func TestRetrieveSimilarSnippetsPrioritizesShortCodeIdentifierOverlap(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "Source file: module_context.py", "class Errors:\n    def module_ctx(self, name):\n        return name\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "py", "Source file: generic_errors.py", "def handle_errors(errors, name):\n    try:\n        return name\n    except Exception:\n        return errors\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "py", "Source file: unrelated.py", "def unrelated(value):\n    return value\n"); err != nil {
		t.Fatal(err)
	}

	query := "errors = Errors()\ntry:\n    with errors.module_ctx(name):"
	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), query, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("got no results")
	}
	if results[0].Topic != "Source file: module_context.py" {
		t.Fatalf("top result = %q, want module_context.py", results[0].Topic)
	}
}

func TestRetrieveSimilarSnippetsWeightsMeaningfulCodeContextTokens(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "Source file: app/generic.py", "def checkout(self):\n    return self.status\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "py", "Source file: app/payment_gateway.py", "class PaymentGateway:\n    def resolve_payment_gateway(self, order_id):\n        return order_id\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "py", "Source file: app/order.py", "class Order:\n    def checkout(self):\n        return self.total\n"); err != nil {
		t.Fatal(err)
	}

	query := "async def checkout(self):\n    return self.resolve_payment_gateway(order_id)"
	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), query, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "app/payment_gateway.py" {
		t.Fatalf("top result = %+v, want app/payment_gateway.py first", results)
	}
}

func TestRetrieveSimilarSnippetsMatchesCamelQueryToSnakeIdentifier(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "Source file: app/cache.py", "def cache_store():\n    return 'hit'\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "py", "Source file: app/noise.py", "def unrelated_store():\n    return 'miss'\n"); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "CacheStore", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "app/cache.py" {
		t.Fatalf("camel-to-snake retrieval = %+v, want app/cache.py first", results)
	}
}

func TestRetrieveSimilarSnippetsBackfillsCodeLikeZeroHitQueries(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "Source file: app/template.py", "def render_template(value):\n    return value\n"); err != nil {
		t.Fatal(err)
	}
	if err := AddKnowledge(db, "py", "Source file: app/parser.py", "def parse_node(value):\n    return value\n"); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "            });\n            elementClose(\"div\");\n            \"\"\",", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("code-like zero-hit query returned %d result(s), want fallback candidates: %+v", len(results), results)
	}
}

func TestRetrieveSimilarSnippetsLeavesNaturalZeroHitQueriesEmpty(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "py", "Source file: app/cache.py", "def cache_store():\n    return 'hit'\n"); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "unmatchednaturalquerytoken", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("natural zero-hit query returned fallback candidates: %+v", results)
	}
}

func TestQueryTokenWeightsBoostRareCandidateTerms(t *testing.T) {
	tokens := []string{"cache", "lease"}
	candidates := []Snippet{
		{Topic: "Source file: cache_a.py", Content: "cache shared helper\n"},
		{Topic: "Source file: cache_b.py", Content: "cache shared helper\n"},
		{Topic: "Source file: lease_cache.py", Content: "cache lease renewal helper\n"},
	}

	weights := queryTokenWeights(tokens, candidates)
	if weights["lease"] <= weights["cache"] {
		t.Fatalf("rare token weight lease=%f, common token weight cache=%f", weights["lease"], weights["cache"])
	}

	commonOnly := lexicalBoost(tokens, weights, candidates[0])
	rareAndCommon := lexicalBoost(tokens, weights, candidates[2])
	if rareAndCommon <= commonOnly {
		t.Fatalf("rare+common boost=%f, common-only boost=%f", rareAndCommon, commonOnly)
	}
}

func TestCandidateBM25BoostsPrioritizeSpecificTermOverlap(t *testing.T) {
	tokens := []string{"cache", "lease", "renewal"}
	candidates := []Snippet{
		{Topic: "Source file: cache_a.py", Content: "cache shared helper\n"},
		{Topic: "Source file: lease_cache.py", Content: "cache lease renewal helper\n"},
	}

	boosts := candidateBM25Boosts(tokens, candidates)
	if len(boosts) != len(candidates) {
		t.Fatalf("boost count = %d, want %d", len(boosts), len(candidates))
	}
	if boosts[1] <= boosts[0] {
		t.Fatalf("specific boost=%f, generic boost=%f; want specific higher", boosts[1], boosts[0])
	}
	if boosts[1] <= 0 {
		t.Fatalf("specific boost=%f, want positive", boosts[1])
	}
}

func TestCandidateExactIdentifierBoostsPrioritizeImportedSymbol(t *testing.T) {
	prompt := strings.Join([]string{
		"from app.constants import DATA_COORDINATORS, DATA_CLIENT",
		"client = hass.data[DOMAIN][DATA_CLIENT]",
	}, "\n")
	candidates := []Snippet{
		{Topic: "Source file: candidate_000.py", Content: "# Identifier: DATA_CLIENT\nDATA_CLIENT = 'client'\n"},
		{Topic: "Source file: candidate_001.py", Content: "# Identifier: DATA_COORDINATORS\nDATA_COORDINATORS = 'coordinators'\n"},
		{Topic: "Source file: candidate_002.py", Content: "# Identifier: OTHER_CONSTANT\nOTHER_CONSTANT = 'ohme'\n"},
	}

	boosts := candidateExactIdentifierBoosts(prompt, candidates)
	if len(boosts) != len(candidates) {
		t.Fatalf("boost count = %d, want %d", len(boosts), len(candidates))
	}
	if boosts[0] <= 0 || boosts[1] <= 0 {
		t.Fatalf("imported identifier boosts should be positive: %+v", boosts)
	}
	if boosts[2] != 0 {
		t.Fatalf("unmentioned identifier boost=%f, want zero", boosts[2])
	}
}

func TestCandidateStructuredPathBoostsUseRankedMetadataPaths(t *testing.T) {
	candidates := []Snippet{
		{ID: 1, Path: "app/service.py"},
		{ID: 2, Path: "app/providers/stripe.py"},
		{ID: 3, Path: "app/unrelated.py"},
	}
	boosts := candidateStructuredPathBoosts(candidates, map[string]int{
		"app/providers/stripe.py": 1,
		"app/service.py":          3,
	}, map[int]bool{1: true})

	if len(boosts) != len(candidates) {
		t.Fatalf("boost count = %d, want %d", len(boosts), len(candidates))
	}
	if boosts[0] != 0 {
		t.Fatalf("primary FTS candidate boost=%f, want zero", boosts[0])
	}
	if boosts[1] <= 0 {
		t.Fatalf("structured-only target boost=%f, want positive", boosts[1])
	}
	if boosts[2] != 0 {
		t.Fatalf("unranked boost=%f, want zero", boosts[2])
	}
}

func TestCandidateValueRankMapRanksPositiveSignals(t *testing.T) {
	candidates := []Snippet{
		{ID: 1},
		{ID: 2},
		{ID: 3},
	}
	ranks := candidateValueRankMap(candidates, []float64{0.2, 0, 0.9}, true)

	if ranks[3] != 1 {
		t.Fatalf("candidate 3 rank = %d, want 1", ranks[3])
	}
	if ranks[1] != 2 {
		t.Fatalf("candidate 1 rank = %d, want 2", ranks[1])
	}
	if _, exists := ranks[2]; exists {
		t.Fatalf("zero-signal candidate was ranked: %+v", ranks)
	}
}

func TestCandidateRankFusionCombinesIndependentSignals(t *testing.T) {
	candidates := []Snippet{
		{ID: 1, Path: "app/generic.py", Score: 0.100},
		{ID: 2, Path: "app/target.py", Score: 0.105},
	}
	applyCandidateRankFusion(
		candidates,
		map[int]int{2: 1},
		map[int]int{2: 1},
		map[int]int{2: 1},
		map[string]int{"app/target.py": 1},
		map[int]int{2: 1},
	)

	if candidates[0].ID != 2 {
		t.Fatalf("fused first candidate ID = %d, want multi-signal candidate 2: %+v", candidates[0].ID, candidates)
	}
	if candidates[0].Score >= candidates[1].Score {
		t.Fatalf("fused target score=%f, generic score=%f; want target lower", candidates[0].Score, candidates[1].Score)
	}
}

func TestCandidateStructuralRerankBoostsUseMetadataEvidence(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ReplaceSymbolsForFile(db, "py", "app/generic.py", []byte("def helper():\n    return 'generic'\n")); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSymbolsForFile(db, "py", "app/payment_gateway.py", []byte("class PaymentGateway:\n    def authorize_payment(self, amount):\n        return amount\n")); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSymbolReferencesForFile(db, "py", "app/checkout.py", []byte("from app.payment_gateway import PaymentGateway\n\n\ndef checkout(amount):\n    return PaymentGateway().authorize_payment(amount)\n")); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceImportsForFile(db, "py", "app/checkout.py", []byte("from app.payment_gateway import PaymentGateway\n")); err != nil {
		t.Fatal(err)
	}

	candidates := []Snippet{
		{ID: 1, Path: "app/generic.py"},
		{ID: 2, Path: "app/payment_gateway.py"},
		{ID: 3, Path: "app/checkout.py"},
	}
	boosts, ranks, err := candidateStructuralRerankBoosts(db, "PaymentGateway authorize_payment checkout", candidates)
	if err != nil {
		t.Fatal(err)
	}

	if boosts[1] <= boosts[0] {
		t.Fatalf("target boost=%f, generic boost=%f; want target boosted by structural evidence", boosts[1], boosts[0])
	}
	if ranks[2] == 0 {
		t.Fatalf("target candidate missing structural rank: %+v", ranks)
	}
	if ranks[3] == 0 {
		t.Fatalf("caller/import candidate missing structural rank: %+v", ranks)
	}
}

func TestRetrieveSimilarSnippetsReranksStructuralSymbolDefinition(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/payment_gateway.py", "class PaymentGateway:\n    def authorize_payment(self, amount):\n        return amount\n")
	writeTestFile(t, root, "app/noise.py", strings.Repeat("payment gateway authorize payment amount checkout workflow\n", 80))
	writeTestFile(t, root, "app/checkout.py", "from app.payment_gateway import PaymentGateway\n\n\ndef checkout(amount):\n    return PaymentGateway().authorize_payment(amount)\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "PaymentGateway authorize_payment checkout", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "app/payment_gateway.py" {
		t.Fatalf("structural rerank results = %+v, want app/payment_gateway.py first", results)
	}
	if !snippetPathsContain(results, "app/checkout.py") {
		t.Fatalf("structural rerank results missing caller/import context: %+v", results)
	}
}

func TestRetrieveSimilarSnippetsUsesExpandedIdentifierFTSPath(t *testing.T) {
	root := t.TempDir()
	for idx := 0; idx < 80; idx++ {
		writeTestFile(t, root, fmt.Sprintf("noise/exact_%03d.py", idx), "refreshtoken getorcreate retry failure\n")
	}
	writeTestFile(t, root, "app/session_cache.py", `
class SessionCache:
    def get_or_create(self, refresh_token):
        """get create refresh token session cache"""
        return refresh_token
`)

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	options := DefaultIndexOptions()
	options.MaxFileBytes = 4096
	if _, err := IndexDirectoryWithOptions(db, root, NewLanguageFilter("python"), options); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "fix getOrCreate refreshToken", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !snippetPathsContain(results, "app/session_cache.py") {
		t.Fatalf("expanded identifier FTS path did not retrieve session cache: %+v", results)
	}

	search, err := SearchMemory(db, mustTestCompressor(t), "fix getOrCreate refreshToken", 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Receipts) == 0 || !receiptForPathHasReason(search.Receipts, "app/session_cache.py", "expanded identifier retrieval path") {
		t.Fatalf("search receipts did not explain expanded identifier retrieval: %+v", search.Receipts)
	}
	if !receiptEvidenceContains(search.Receipts, "expanded identifier retrieval path terms: get, create, refresh, token") {
		t.Fatalf("search receipts missing expanded identifier evidence: %+v", search.Receipts)
	}
}

func TestStructuralRerankSkipsMultiLineCompletionContext(t *testing.T) {
	if !allowStructuralRerank("PaymentGateway authorize_payment checkout") {
		t.Fatal("single-line symbol query should allow structural rerank")
	}
	if allowStructuralRerank("def checkout(amount):\n    return PaymentGateway().authorize_payment(amount)") {
		t.Fatal("multi-line completion context should stay on lexical/QND ranking")
	}
	if allowStructuralRerank("vertical_speed_indicator = VerticalSpeedIndicator(fdmexec)") {
		t.Fatal("single-line completion fragment should stay on lexical/QND ranking")
	}
}

func TestCandidateJaccardRankMapUsesTokenOverlap(t *testing.T) {
	candidates := []Snippet{
		{ID: 1, Topic: "Source file: app/generic.py", Content: "cache shared helper\n"},
		{ID: 2, Topic: "Source file: app/target.py", Content: "lease renewal cache store\n"},
	}
	ranks := candidateJaccardRankMap([]string{"cache", "lease", "renewal"}, candidates)

	if ranks[2] != 1 {
		t.Fatalf("target rank = %d, want 1: %+v", ranks[2], ranks)
	}
	if ranks[1] != 2 {
		t.Fatalf("generic rank = %d, want 2: %+v", ranks[1], ranks)
	}
}

func TestApplyTailRankCoveragePreservesTopThreeAndFillsTail(t *testing.T) {
	candidates := []Snippet{
		{ID: 1, Path: "one.py", Score: 0.1},
		{ID: 2, Path: "two.py", Score: 0.2},
		{ID: 3, Path: "three.py", Score: 0.3},
		{ID: 4, Path: "four.py", Score: 0.4},
		{ID: 5, Path: "five.py", Score: 0.5},
		{ID: 6, Path: "six.py", Score: 0.6},
	}

	ranked := applyTailRankCoverage(candidates, map[int]int{6: 1, 5: 2}, 5)

	if len(ranked) < 5 {
		t.Fatalf("ranked count = %d, want at least 5: %+v", len(ranked), ranked)
	}
	for idx, want := range []int{1, 2, 3} {
		if ranked[idx].ID != want {
			t.Fatalf("ranked[%d] = %d, want preserved top-three ID %d: %+v", idx, ranked[idx].ID, want, ranked)
		}
	}
	if ranked[3].ID != 6 || ranked[4].ID != 5 {
		t.Fatalf("tail coverage IDs = %d,%d; want 6,5: %+v", ranked[3].ID, ranked[4].ID, ranked)
	}
}

func TestCombinedLexicalCoverageRankMapIncludesBM25OnlySignals(t *testing.T) {
	ranks := combinedLexicalCoverageRankMap(
		map[int]int{2: 1, 4: 3},
		map[int]int{3: 1, 4: 3},
	)

	if ranks[4] != 1 {
		t.Fatalf("consensus candidate rank = %d, want 1: %+v", ranks[4], ranks)
	}
	if ranks[2] == 0 || ranks[3] == 0 {
		t.Fatalf("single-signal lexical candidates missing from coverage ranks: %+v", ranks)
	}
}

func TestRankSearchCandidatesPreservesProtectedPrimaryCandidate(t *testing.T) {
	candidates := []Snippet{
		{ID: 2, Path: "second.py", Score: 0.1},
		{ID: 3, Path: "third.py", Score: 0.2},
		{ID: 1, Path: "protected.py", Score: 0.3},
	}
	ranked := rankSearchCandidates(candidates, map[int]bool{1: true, 2: true, 3: true}, 1, nil, 2)

	if len(ranked) != 2 {
		t.Fatalf("ranked %d candidates, want 2: %+v", len(ranked), ranked)
	}
	if ranked[0].ID != 1 {
		t.Fatalf("first ranked candidate ID = %d, want protected ID 1: %+v", ranked[0].ID, ranked)
	}
	if ranked[1].ID == ranked[0].ID {
		t.Fatalf("protected candidate was duplicated: %+v", ranked)
	}
}

func TestRankSearchCandidatesDiversifiesDuplicatePaths(t *testing.T) {
	candidates := []Snippet{
		{ID: 1, Path: "app/cache.py", Score: 0.1},
		{ID: 2, Path: "app/cache.py", Score: 0.2},
		{ID: 3, Path: "app/service.py", Score: 0.3},
	}
	ranked := rankSearchCandidates(candidates, map[int]bool{1: true, 2: true, 3: true}, 1, nil, 2)

	if len(ranked) != 2 {
		t.Fatalf("ranked %d candidates, want 2: %+v", len(ranked), ranked)
	}
	if ranked[0].ID != 1 {
		t.Fatalf("first ranked candidate ID = %d, want protected ID 1: %+v", ranked[0].ID, ranked)
	}
	if ranked[1].Path != "app/service.py" {
		t.Fatalf("second candidate path = %q, want diverse service path: %+v", ranked[1].Path, ranked)
	}
}

func TestStructureBoostUsesQueryDeclarationIntent(t *testing.T) {
	if got := queryStructureIntent("\n\ndef build_cache():"); got != "function" {
		t.Fatalf("function intent = %q", got)
	}
	if got := queryStructureIntent("    def fetch(self):"); got != "method" {
		t.Fatalf("method intent = %q", got)
	}
	if got := queryStructureIntent("class CacheStore:"); got != "class" {
		t.Fatalf("class intent = %q", got)
	}

	if weight := queryStructureBoostWeight("def build_cache():", []string{"def", "build", "cache"}); weight != 0.05 {
		t.Fatalf("simple declaration weight = %f, want 0.05", weight)
	}
	if weight := queryStructureBoostWeight("def build_cache():\n    \"\"\"Build it.\"\"\"", []string{"def", "build", "cache"}); weight != 0 {
		t.Fatalf("docstring declaration weight = %f, want 0", weight)
	}

	functionBoost := structureBoost("function", "def build_cache():\n    return CacheStore()\n", 0.10)
	classBoost := structureBoost("function", "class CacheStore:\n    pass\n", 0.10)
	if functionBoost <= classBoost {
		t.Fatalf("function boost=%f, class boost=%f; want function higher", functionBoost, classBoost)
	}

	methodBoost := structureBoost("method", "    def fetch(self):\n        return True\n", 0.10)
	topLevelFunctionBoost := structureBoost("method", "def fetch(store):\n    return True\n", 0.10)
	if methodBoost <= topLevelFunctionBoost {
		t.Fatalf("method boost=%f, top-level boost=%f; want method higher", methodBoost, topLevelFunctionBoost)
	}
}

func TestRetrieveSimilarSnippetsExpandsResolvedImportTargets(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app/service.py", "from app.providers.stripe import PaymentProvider as BillingGateway\n\n\ndef checkout():\n    return BillingGateway()\n")
	writeTestFile(t, root, "app/providers/stripe.py", "class PaymentProvider:\n    def charge(self, amount):\n        return amount\n")

	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := IndexDirectory(db, root, NewLanguageFilter("python")); err != nil {
		t.Fatal(err)
	}

	results, err := RetrieveSimilarSnippets(db, mustTestCompressor(t), "BillingGateway checkout workflow", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !snippetPathsContain(results, "app/providers/stripe.py") {
		t.Fatalf("resolved import target was not retrieved: %+v", results)
	}
}

func snippetPathsContain(snippets []Snippet, path string) bool {
	for _, snippet := range snippets {
		if snippet.Path == path {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func tokenPathContainsAll(paths [][]string, wants ...string) bool {
	for _, path := range paths {
		matches := true
		for _, want := range wants {
			if !stringSliceContains(path, want) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
