package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTEnt/SnapZip/core"
)

func TestCLIInitSearchStatsAndReset(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	versionOutput := runSnapZip(t, repoRoot, "version")
	if !strings.Contains(versionOutput, "snapzip dev") || !strings.Contains(versionOutput, "commit: unknown") {
		t.Fatalf("version output missing default metadata:\n%s", versionOutput)
	}

	versionJSON := runSnapZip(t, repoRoot, "version", "--json")
	var versionPayload versionInfo
	if err := json.Unmarshal([]byte(versionJSON), &versionPayload); err != nil {
		t.Fatalf("version --json returned invalid JSON: %v\n%s", err, versionJSON)
	}
	if versionPayload.Version != "dev" || versionPayload.Commit != "unknown" {
		t.Fatalf("version --json returned wrong defaults: %+v", versionPayload)
	}

	fixture := t.TempDir()
	writeCLIFile(t, fixture, "web/index.html", "<main>SnapZip HTML fixture</main>\n")
	writeCLIFile(t, fixture, "web/site.css", ".snapzip { color: #123456; }\n")
	writeCLIFile(t, fixture, "lib/cache.rb", "require \"json\"\n\nclass CacheStore\nend\n")
	writeCLIFile(t, fixture, "node_modules/ignored/skip.rb", "class IgnoredDependency\nend\n")

	dbDir := t.TempDir()
	runSnapZip(t, repoRoot,
		"init-db",
		"--db-dir", dbDir,
		"--langs", "popular",
		"--crawl", fixture,
	)

	searchOutput := runSnapZip(t, repoRoot,
		"search",
		"--db-dir", dbDir,
		"--query", "ruby error handling CacheStore",
		"--limit", "1",
	)
	if !strings.Contains(searchOutput, "CacheStore") {
		t.Fatalf("search output did not include indexed Ruby fixture:\n%s", searchOutput)
	}
	if !strings.Contains(searchOutput, "## Context Receipts") {
		t.Fatalf("search output did not include retrieval receipts:\n%s", searchOutput)
	}

	searchJSON := runSnapZip(t, repoRoot,
		"search",
		"--db-dir", dbDir,
		"--query", "ruby error handling CacheStore",
		"--limit", "1",
		"--json",
	)
	var searchPayload struct {
		Query    string         `json:"query"`
		Snippets []core.Snippet `json:"snippets"`
	}
	if err := json.Unmarshal([]byte(searchJSON), &searchPayload); err != nil {
		t.Fatalf("search --json returned invalid JSON: %v\n%s", err, searchJSON)
	}
	if searchPayload.Query == "" || len(searchPayload.Snippets) != 1 || !strings.Contains(searchPayload.Snippets[0].Content, "CacheStore") {
		t.Fatalf("search --json did not include expected snippet:\n%s", searchJSON)
	}
	if searchPayload.Snippets[0].Diagnostics != nil {
		t.Fatalf("search --json unexpectedly included diagnostics without --diagnostics: %+v", searchPayload.Snippets[0].Diagnostics)
	}

	searchDiagnosticsJSON := runSnapZip(t, repoRoot,
		"search",
		"--db-dir", dbDir,
		"--query", "ruby error handling CacheStore",
		"--limit", "1",
		"--json",
		"--diagnostics",
	)
	var searchDiagnosticsPayload struct {
		Snippets []core.Snippet `json:"snippets"`
	}
	if err := json.Unmarshal([]byte(searchDiagnosticsJSON), &searchDiagnosticsPayload); err != nil {
		t.Fatalf("search --json --diagnostics returned invalid JSON: %v\n%s", err, searchDiagnosticsJSON)
	}
	if len(searchDiagnosticsPayload.Snippets) != 1 || searchDiagnosticsPayload.Snippets[0].Diagnostics == nil {
		t.Fatalf("search --json --diagnostics did not include snippet diagnostics:\n%s", searchDiagnosticsJSON)
	}
	diagnostics := searchDiagnosticsPayload.Snippets[0].Diagnostics
	if diagnostics.FinalRank != 1 || diagnostics.QND <= 0 || len(diagnostics.MatchedQueryTokens) == 0 {
		t.Fatalf("search diagnostics missing rank, QND, or matched query tokens: %+v", diagnostics)
	}

	packOutput := runSnapZip(t, repoRoot,
		"pack",
		"--db-dir", dbDir,
		"--query", "ruby CacheStore",
		"--limit", "1",
		"--budget", "2048",
	)
	if !strings.Contains(packOutput, "# SnapZip Context Pack") || !strings.Contains(packOutput, "CacheStore") || !strings.Contains(packOutput, "## Context Quality") {
		t.Fatalf("pack output did not include expected context:\n%s", packOutput)
	}

	statsOutput := runSnapZip(t, repoRoot, "stats", "--db-dir", dbDir)
	if !strings.Contains(statsOutput, "knowledge rows: 3") {
		t.Fatalf("stats output did not show three indexed files:\n%s", statsOutput)
	}
	if !strings.Contains(statsOutput, "feedback rows: 0") {
		t.Fatalf("search query polluted feedback memory:\n%s", statsOutput)
	}

	statsJSON := runSnapZip(t, repoRoot, "stats", "--db-dir", dbDir, "--json")
	var statsPayload core.DatabaseStats
	if err := json.Unmarshal([]byte(statsJSON), &statsPayload); err != nil {
		t.Fatalf("stats --json returned invalid JSON: %v\n%s", err, statsJSON)
	}
	if statsPayload.KnowledgeRows != 3 || statsPayload.FeedbackRows != 0 || statsPayload.ImportRows == 0 {
		t.Fatalf("stats --json returned wrong counts: %+v", statsPayload)
	}

	ignoredOutput := runSnapZip(t, repoRoot,
		"search",
		"--db-dir", dbDir,
		"--query", "ruby IgnoredDependency",
		"--limit", "1",
	)
	if !strings.Contains(ignoredOutput, "Found 0 matching snippets") {
		t.Fatalf("search unexpectedly found skipped dependency content:\n%s", ignoredOutput)
	}

	runSnapZip(t, repoRoot,
		"init-db",
		"--db-dir", dbDir,
		"--langs", "popular",
		"--crawl", fixture,
	)
	statsOutput = runSnapZip(t, repoRoot, "stats", "--db-dir", dbDir)
	if !strings.Contains(statsOutput, "knowledge rows: 3") {
		t.Fatalf("re-indexing duplicated rows:\n%s", statsOutput)
	}

	resetFixture := t.TempDir()
	writeCLIFile(t, resetFixture, "fresh.rb", "class FreshStart\nend\n")
	runSnapZip(t, repoRoot,
		"init-db",
		"--db-dir", dbDir,
		"--langs", "ruby",
		"--crawl", resetFixture,
		"--reset",
	)
	statsOutput = runSnapZip(t, repoRoot, "stats", "--db-dir", dbDir)
	if !strings.Contains(statsOutput, "knowledge rows: 1") {
		t.Fatalf("reset did not leave a fresh one-file index:\n%s", statsOutput)
	}

	configRoot := t.TempDir()
	initConfigOutput := runSnapZip(t, repoRoot, "init-config", "--dir", configRoot)
	if !strings.Contains(initConfigOutput, "Wrote") {
		t.Fatalf("init-config did not write starter config:\n%s", initConfigOutput)
	}
	initConfigOutput = runSnapZip(t, repoRoot, "init-config", "--dir", configRoot)
	if !strings.Contains(initConfigOutput, "Skipped existing") {
		t.Fatalf("init-config did not skip existing config:\n%s", initConfigOutput)
	}
}

func TestCLIVersionBuildMetadata(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(
		"go",
		"run",
		"-ldflags",
		"-X main.version=vtest -X main.commit=abc123 -X main.date=2026-06-20T00:00:00Z",
		"./cmd/snapzip",
		"version",
		"--json",
	)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("snapzip version with ldflags failed: %v\n%s", err, string(output))
	}
	var payload versionInfo
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("version --json returned invalid JSON: %v\n%s", err, string(output))
	}
	if payload.Version != "vtest" || payload.Commit != "abc123" || payload.Date != "2026-06-20T00:00:00Z" {
		t.Fatalf("version metadata not stamped from ldflags: %+v", payload)
	}
}

func TestMCPServerExposesSearchTool(t *testing.T) {
	fixture := t.TempDir()
	writeCLIFile(t, fixture, "lib/helper.rb", "module CacheHelper\nend\n")
	writeCLIFile(t, fixture, "lib/cache.rb", "require \"json\"\nrequire_relative \"helper\"\n\nclass CacheStore\nend\n\ndef build_cache\n  CacheStore.new\nend\n")
	writeCLIFile(t, fixture, "test/cache_test.rb", "def test_cache\n  build_cache()\nend\n")

	dbDir := t.TempDir()
	db, err := core.InitDB(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.IndexDirectory(db, fixture, core.NewLanguageFilter("ruby")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"ruby CacheStore","limit":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"context_pack","arguments":{"query":"ruby CacheStore","limit":1,"budget":2048}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"repair_pack","arguments":{"error_output":"lib/cache.rb:1: uninitialized constant CacheStore","limit":2,"budget":4096}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"affected_tests","arguments":{"path":"lib/cache.rb","limit":5}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"symbol_context","arguments":{"query":"build_cache","limit":5}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"imports","arguments":{"query":"helper","limit":5}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"graph","arguments":{"path":"lib/helper.rb","limit":5}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"validation_plan","arguments":{"path":"lib/cache.rb","limit":5}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"pr_context","arguments":{"path":"lib/cache.rb","limit":5,"budget":4096}}}`,
	}, "\n") + "\n"

	var output bytes.Buffer
	if err := runMCPServer(strings.NewReader(input), &output, dbDir); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 11 {
		t.Fatalf("got %d MCP responses, want 11:\n%s", len(lines), output.String())
	}
	if !strings.Contains(lines[1], `"tools"`) || !strings.Contains(lines[1], `"context_pack"`) || !strings.Contains(lines[1], `"repair_pack"`) || !strings.Contains(lines[1], `"symbol_context"`) || !strings.Contains(lines[1], `"imports"`) || !strings.Contains(lines[1], `"graph"`) || !strings.Contains(lines[1], `"validation_plan"`) || !strings.Contains(lines[1], `"pr_context"`) {
		t.Fatalf("tools/list did not expose expected tools:\n%s", lines[1])
	}
	if !strings.Contains(lines[0], `"version":"dev"`) {
		t.Fatalf("initialize response missing server version:\n%s", lines[0])
	}
	if !strings.Contains(lines[2], "CacheStore") {
		t.Fatalf("search tool response did not include indexed content:\n%s", lines[2])
	}
	if !strings.Contains(lines[3], "SnapZip Context Pack") || !strings.Contains(lines[3], "CacheStore") {
		t.Fatalf("context_pack tool response did not include expected context:\n%s", lines[3])
	}
	if !strings.Contains(lines[4], "Context Receipts") || !strings.Contains(lines[4], "lib/cache.rb") {
		t.Fatalf("repair_pack tool response did not include receipt-backed context:\n%s", lines[4])
	}
	if !strings.Contains(lines[5], "test/cache_test.rb") {
		t.Fatalf("affected_tests tool response missing direct test:\n%s", lines[5])
	}
	if !strings.Contains(lines[6], "SnapZip Symbol Context") || !strings.Contains(lines[6], "test/cache_test.rb") {
		t.Fatalf("symbol_context tool response missing reference context:\n%s", lines[6])
	}
	if !strings.Contains(lines[7], "SnapZip Import Context") || !strings.Contains(lines[7], "lib/cache.rb") || !strings.Contains(lines[7], "lib/helper.rb") {
		t.Fatalf("imports tool response missing import context:\n%s", lines[7])
	}
	if !strings.Contains(lines[8], "SnapZip Dependency Graph") || !strings.Contains(lines[8], "Imported By") || !strings.Contains(lines[8], "lib/cache.rb") {
		t.Fatalf("graph tool response missing dependency context:\n%s", lines[8])
	}
	if !strings.Contains(lines[9], "SnapZip Validation Plan") || !strings.Contains(lines[9], "bundle exec rake test") {
		t.Fatalf("validation_plan tool response missing suggested validation:\n%s", lines[9])
	}
	if !strings.Contains(lines[10], "SnapZip PR Context") || !strings.Contains(lines[10], "Mode: review") || !strings.Contains(lines[10], "test/cache_test.rb") {
		t.Fatalf("pr_context tool response missing review context:\n%s", lines[10])
	}
}

func TestMCPLazySyncThrottle(t *testing.T) {
	mcpLazySyncMu.Lock()
	mcpLazySyncSeen = map[string]time.Time{}
	mcpLazySyncMu.Unlock()

	dbDir := t.TempDir()
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	if !shouldRunMCPLazySync(dbDir, now) {
		t.Fatal("first lazy sync should run")
	}
	if shouldRunMCPLazySync(dbDir, now.Add(time.Second)) {
		t.Fatal("lazy sync should be throttled inside interval")
	}
	if !shouldRunMCPLazySync(dbDir, now.Add(mcpLazySyncInterval+time.Second)) {
		t.Fatal("lazy sync should run again after interval")
	}
}

func TestCLIAdvancedContextCommands(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	fixture := t.TempDir()
	writeCLIFile(t, fixture, "go.mod", "module snapzipfixture\n\ngo 1.25\n")
	writeCLIFile(t, fixture, ".snapzip/config.toml", "[validation]\ncommand = \"go test ./...\"\n\n[validation.commands]\ngo = \"go test ./pkg\"\n")
	writeCLIFile(t, fixture, "pkg/store/store.go", "package store\n\ntype Store struct{}\n")
	writeCLIFile(t, fixture, "pkg/cache.go", "package cache\n\nimport \"snapzipfixture/pkg/store\"\n\ntype CacheStore struct{ Store store.Store }\n\nfunc NewCacheStore() CacheStore { return CacheStore{Store: store.Store{}} }\n")
	writeCLIFile(t, fixture, "pkg/cache_test.go", "package cache\n\nimport \"testing\"\n\nfunc TestConstructor(t *testing.T) { _ = NewCacheStore() }\n")
	gitInit := exec.Command("git", "init")
	gitInit.Dir = fixture
	if output, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(output))
	}

	dbDir := t.TempDir()
	runSnapZip(t, repoRoot,
		"index",
		"--db-dir", dbDir,
		"--langs", "go",
		"--crawl", fixture,
	)

	mapOutput := runSnapZip(t, repoRoot, "map", "--db-dir", dbDir, "--limit", "10")
	if !strings.Contains(mapOutput, "NewCacheStore") {
		t.Fatalf("map output missing symbol:\n%s", mapOutput)
	}

	symbolOutput := runSnapZip(t, repoRoot, "symbols", "--db-dir", dbDir, "--query", "CacheStore", "--limit", "5")
	if !strings.Contains(symbolOutput, "pkg/cache.go") {
		t.Fatalf("symbols output missing source path:\n%s", symbolOutput)
	}

	symbolContextOutput := runSnapZip(t, repoRoot, "symbol-context", "--db-dir", dbDir, "--query", "NewCacheStore", "--limit", "5")
	if !strings.Contains(symbolContextOutput, "Definitions") || !strings.Contains(symbolContextOutput, "References") || !strings.Contains(symbolContextOutput, "pkg/cache_test.go") {
		t.Fatalf("symbol-context output missing definition/reference context:\n%s", symbolContextOutput)
	}

	importsOutput := runSnapZip(t, repoRoot, "imports", "--db-dir", dbDir, "--query", "pkg/store", "--limit", "5")
	if !strings.Contains(importsOutput, "SnapZip Import Context") || !strings.Contains(importsOutput, "pkg/cache.go") || !strings.Contains(importsOutput, "pkg/store/store.go") {
		t.Fatalf("imports output missing resolved Go package import:\n%s", importsOutput)
	}

	graphOutput := runSnapZip(t, repoRoot, "graph", "--db-dir", dbDir, "--path", "pkg/store/store.go", "--limit", "5")
	if !strings.Contains(graphOutput, "SnapZip Dependency Graph") || !strings.Contains(graphOutput, "Imported By") || !strings.Contains(graphOutput, "pkg/cache.go") {
		t.Fatalf("graph output missing incoming Go package import:\n%s", graphOutput)
	}
	graphJSON := runSnapZip(t, repoRoot, "graph", "--db-dir", dbDir, "--path", "pkg/store/store.go", "--limit", "5", "--json")
	var graphPayload core.DependencyGraph
	if err := json.Unmarshal([]byte(graphJSON), &graphPayload); err != nil {
		t.Fatalf("graph --json returned invalid JSON: %v\n%s", err, graphJSON)
	}
	if len(graphPayload.ImportedBy) == 0 || graphPayload.ImportedBy[0].Path != "pkg/cache.go" {
		t.Fatalf("graph --json missing incoming Go package import: %+v", graphPayload)
	}

	relatedOutput := runSnapZip(t, repoRoot, "related", "--db-dir", dbDir, "--path", "pkg/cache.go", "--limit", "5")
	if !strings.Contains(relatedOutput, "pkg/cache_test.go") {
		t.Fatalf("related output missing test file:\n%s", relatedOutput)
	}

	validatePlanOutput := runSnapZip(t, repoRoot, "validate", "--db-dir", dbDir, "--path", "pkg/cache.go", "--dir", fixture, "--limit", "5")
	if !strings.Contains(validatePlanOutput, "# SnapZip Validate") || !strings.Contains(validatePlanOutput, "pkg/cache_test.go") || !strings.Contains(validatePlanOutput, "configured validation command for go") {
		t.Fatalf("validate plan output missing affected test or command:\n%s", validatePlanOutput)
	}

	prOutput := runSnapZip(t, repoRoot, "pr", "--db-dir", dbDir, "--path", "pkg/cache.go", "--dir", fixture, "--limit", "5", "--budget", "4096")
	if !strings.Contains(prOutput, "# SnapZip PR Context") || !strings.Contains(prOutput, "pkg/cache.go") || !strings.Contains(prOutput, "pkg/cache_test.go") || !strings.Contains(prOutput, "Mode: review") {
		t.Fatalf("pr output missing review context:\n%s", prOutput)
	}
	prJSON := runSnapZip(t, repoRoot, "pr", "--db-dir", dbDir, "--path", "pkg/cache.go", "--dir", fixture, "--limit", "5", "--budget", "4096", "--json")
	var prPayload prReport
	if err := json.Unmarshal([]byte(prJSON), &prPayload); err != nil {
		t.Fatalf("pr --json returned invalid JSON: %v\n%s", err, prJSON)
	}
	if len(prPayload.ChangedFiles) == 0 || prPayload.Pack == nil || prPayload.Pack.Mode != "review" {
		t.Fatalf("pr --json missing changed files or review pack: %+v", prPayload)
	}
	prChangedOutput := runSnapZip(t, repoRoot, "pr", "--db-dir", dbDir, "--changed", "--dir", fixture, "--limit", "5", "--budget", "4096")
	if !strings.Contains(prChangedOutput, "Base: `working tree`") || !strings.Contains(prChangedOutput, "pkg/cache.go") {
		t.Fatalf("pr --changed output missing working tree diff context:\n%s", prChangedOutput)
	}

	validateRunOutput := runSnapZip(t, repoRoot, "validate", "--db-dir", dbDir, "--path", "pkg/cache.go", "--cmd", "go test ./...", "--dir", fixture, "--limit", "5")
	if !strings.Contains(validateRunOutput, "Status: passed") || !strings.Contains(validateRunOutput, "Exit code: 0") {
		t.Fatalf("validate run output missing passing command result:\n%s", validateRunOutput)
	}

	validateConfigRunOutput := runSnapZip(t, repoRoot, "validate", "--db-dir", dbDir, "--path", "pkg/cache.go", "--run-config", "--dir", fixture, "--limit", "5")
	if !strings.Contains(validateConfigRunOutput, "Command: `go test ./pkg`") || !strings.Contains(validateConfigRunOutput, "Status: passed") {
		t.Fatalf("validate --run-config did not execute configured command:\n%s", validateConfigRunOutput)
	}

	packOutput := runSnapZip(t, repoRoot, "pack", "--db-dir", dbDir, "--query", "CacheStore", "--mode", "test", "--limit", "3", "--budget", "4096")
	if !strings.Contains(packOutput, "Mode: test") || !strings.Contains(packOutput, "pkg/cache_test.go") {
		t.Fatalf("mode-specific pack output missing expected context:\n%s", packOutput)
	}
	if !strings.Contains(packOutput, "## Context Quality") {
		t.Fatalf("mode-specific pack output missing context quality:\n%s", packOutput)
	}

	errorFile := filepath.Join(t.TempDir(), "failure.txt")
	if err := os.WriteFile(errorFile, []byte("pkg/cache_test.go:3: undefined: CacheStore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	repairOutput := runSnapZip(t, repoRoot, "repair-pack", "--db-dir", dbDir, "--error-file", errorFile, "--budget", "4096")
	if !strings.Contains(repairOutput, "SnapZip Context Pack") || !strings.Contains(repairOutput, "Context Receipts") || !strings.Contains(repairOutput, "cache") {
		t.Fatalf("repair-pack output missing failure context:\n%s", repairOutput)
	}
	repairJSON := runSnapZip(t, repoRoot, "repair-pack", "--db-dir", dbDir, "--error-file", errorFile, "--budget", "4096", "--json")
	var repairPayload core.ContextPack
	if err := json.Unmarshal([]byte(repairJSON), &repairPayload); err != nil {
		t.Fatalf("repair-pack --json returned invalid JSON: %v\n%s", err, repairJSON)
	}
	if len(repairPayload.Receipts) == 0 {
		t.Fatalf("repair-pack --json missing receipts:\n%s", repairJSON)
	}
	if repairPayload.Quality.Score <= 0 {
		t.Fatalf("repair-pack --json missing quality score:\n%s", repairJSON)
	}

	affectedOutput := runSnapZip(t, repoRoot, "affected", "--db-dir", dbDir, "--path", "pkg/cache.go")
	if !strings.Contains(affectedOutput, "pkg/cache_test.go") {
		t.Fatalf("affected output missing likely test:\n%s", affectedOutput)
	}

	diagnoseJSON := runSnapZip(t, repoRoot, "diagnose", "--db-dir", dbDir, "--cmd", "printf 'pkg/cache_test.go:3: undefined: CacheStore\\n'; exit 1", "--json")
	var diagnosePayload diagnoseReport
	if err := json.Unmarshal([]byte(diagnoseJSON), &diagnosePayload); err != nil {
		t.Fatalf("diagnose --json returned invalid JSON: %v\n%s", err, diagnoseJSON)
	}
	if diagnosePayload.Passed || diagnosePayload.Pack == nil || len(diagnosePayload.Pack.Receipts) == 0 {
		t.Fatalf("diagnose --json missing failure pack: %+v", diagnosePayload)
	}

	auditOutput := runSnapZip(t, repoRoot, "audit", "--db-dir", dbDir)
	if !strings.Contains(auditOutput, "memory.db gitignore") || !strings.Contains(auditOutput, ".snapzipignore") || !strings.Contains(auditOutput, "MCP") {
		t.Fatalf("audit output missing expected checks:\n%s", auditOutput)
	}

	agentDir := t.TempDir()
	installOutput := runSnapZip(t, repoRoot, "install-agent", "codex", "--dir", agentDir)
	if !strings.Contains(installOutput, "AGENTS.md") {
		t.Fatalf("install-agent did not report AGENTS.md:\n%s", installOutput)
	}
	agentBytes, err := os.ReadFile(filepath.Join(agentDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentBytes), "snapzip pack") {
		t.Fatalf("AGENTS.md missing SnapZip pack rule:\n%s", string(agentBytes))
	}
}

func TestIndexChangedIncludesUntrackedFiles(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	fixture := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = fixture
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(output))
	}
	writeCLIFile(t, fixture, "pkg/new_cache.go", "package cache\n\nfunc NewUntrackedCache() {}\n")
	writeCLIFile(t, fixture, "memory.db", "local generated db")
	writeCLIFile(t, fixture, ".snapzip-ci/memory.db", "local generated db")

	changedFiles, err := gitChangedFiles(fixture, "")
	if err != nil {
		t.Fatal(err)
	}
	changedList := "\n" + strings.Join(changedFiles, "\n") + "\n"
	if !strings.Contains(changedList, "\npkg/new_cache.go\n") {
		t.Fatalf("changed files missing untracked source: %+v", changedFiles)
	}
	if strings.Contains(changedList, "\nmemory.db\n") || strings.Contains(changedList, "\n.snapzip-ci/memory.db\n") {
		t.Fatalf("changed files included generated SnapZip DB: %+v", changedFiles)
	}

	dbDir := t.TempDir()
	runSnapZip(t, repoRoot,
		"index",
		"--db-dir", dbDir,
		"--langs", "go",
		"--crawl", fixture,
		"--changed",
	)

	symbolOutput := runSnapZip(t, repoRoot, "symbols", "--db-dir", dbDir, "--query", "NewUntrackedCache")
	if !strings.Contains(symbolOutput, "NewUntrackedCache") {
		t.Fatalf("changed index did not include untracked file:\n%s", symbolOutput)
	}
}

func TestRepositoryGitHubActionArtifacts(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	action := readRepoFile(t, repoRoot, "action.yml")
	for _, want := range []string{
		"using: composite",
		"cd \"${GITHUB_ACTION_PATH}\"",
		"go build -o",
		"snapzip index --reset",
		"pr_args=(pr",
		"report_path=",
		"json_path=",
	} {
		if !strings.Contains(action, want) {
			t.Fatalf("action.yml missing %q:\n%s", want, action)
		}
	}

	workflow := readRepoFile(t, repoRoot, ".github/workflows/snapzip-pr-context.yml")
	for _, want := range []string{
		"actions/checkout@v7",
		"actions/setup-go@v6",
		"uses: ./",
		"actions/upload-artifact@v7",
		"steps.snapzip.outputs.report_path",
		"steps.snapzip.outputs.json_path",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("snapzip-pr-context workflow missing %q:\n%s", want, workflow)
		}
	}
	if strings.Contains(workflow, "outputs.report-path") || strings.Contains(workflow, "node"+"20") {
		t.Fatalf("snapzip-pr-context workflow contains stale output or runtime reference:\n%s", workflow)
	}

	readme := readRepoFile(t, repoRoot, "README.md")
	for _, want := range []string{
		"### GitHub Action",
		"MTEnt/SnapZip@main",
		"steps.snapzip.outputs.report_path",
		"steps.snapzip.outputs.json_path",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing GitHub Action guidance %q", want)
		}
	}
}

func TestRepositoryPackagingAndDemoAssets(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	releaseWorkflow := readRepoFile(t, repoRoot, ".github/workflows/release.yml")
	if !strings.Contains(releaseWorkflow, "sha256sum * > checksums.txt") {
		t.Fatalf("release workflow does not generate checksums:\n%s", releaseWorkflow)
	}
	if !strings.Contains(releaseWorkflow, "-X main.version=${GITHUB_REF_NAME}") || !strings.Contains(releaseWorkflow, "-X main.commit=${GITHUB_SHA}") {
		t.Fatalf("release workflow does not stamp version metadata:\n%s", releaseWorkflow)
	}
	if !strings.Contains(releaseWorkflow, "--repo \"${GITHUB_REPOSITORY}\"") {
		t.Fatalf("release workflow does not pass explicit repo to gh release:\n%s", releaseWorkflow)
	}
	gitignore := readRepoFile(t, repoRoot, ".gitignore")
	for _, want := range []string{"memory.db-*", ".snapzip-ci/", ".snapzip-pr-context.*"} {
		if !strings.Contains(gitignore, want) {
			t.Fatalf(".gitignore missing generated artifact pattern %q:\n%s", want, gitignore)
		}
	}

	formula := readRepoFile(t, repoRoot, "packaging/homebrew/snapzip.rb")
	for _, want := range []string{
		"class Snapzip < Formula",
		"url \"https://github.com/MTEnt/SnapZip/archive/refs/tags/v0.1.0.tar.gz\"",
		"sha256 \"d541ae58c92feb50a06dca7e32940a10afbb2d3be9e769e4b819742c82779f98\"",
		"head \"https://github.com/MTEnt/SnapZip.git\"",
		"depends_on \"go\" => :build",
		"system \"go\", \"build\"",
		"assert_match \"knowledge rows: 1\"",
		"assert_match \"snapzip v#{version}\"",
	} {
		if !strings.Contains(formula, want) {
			t.Fatalf("Homebrew formula missing %q:\n%s", want, formula)
		}
	}

	packagingReadme := readRepoFile(t, repoRoot, "packaging/homebrew/README.md")
	if !strings.Contains(packagingReadme, "brew tap MTEnt/snapzip") || !strings.Contains(packagingReadme, "brew install --HEAD") || !strings.Contains(packagingReadme, "snapzip version") {
		t.Fatalf("Homebrew packaging README missing install or checksum guidance:\n%s", packagingReadme)
	}

	demoReadme := readRepoFile(t, repoRoot, "examples/review_demo/README.md")
	for _, want := range []string{"snapzip pr --db-dir", "--changed", "--mode review"} {
		if !strings.Contains(demoReadme, want) {
			t.Fatalf("review demo README missing %q:\n%s", want, demoReadme)
		}
	}
	demoConfig := readRepoFile(t, repoRoot, "examples/review_demo/.snapzip/config.toml")
	if !strings.Contains(demoConfig, "python -m pytest tests") {
		t.Fatalf("review demo config missing pytest validation command:\n%s", demoConfig)
	}

	ciWorkflow := readRepoFile(t, repoRoot, ".github/workflows/ci.yml")
	if !strings.Contains(ciWorkflow, "examples/review_demo/app/cache.py") || !strings.Contains(ciWorkflow, "examples/review_demo/tests/test_cache.py") {
		t.Fatalf("CI workflow does not compile review demo files:\n%s", ciWorkflow)
	}
	for _, want := range []string{
		"benchmarks/analyze_repobench.py",
		"benchmarks/learn_ranker.py",
		"benchmarks/promote_diagnostics.py",
		"benchmarks/tune_diagnostics.py",
	} {
		if !strings.Contains(ciWorkflow, want) {
			t.Fatalf("CI workflow does not compile benchmark helper %q:\n%s", want, ciWorkflow)
		}
	}
	if !strings.Contains(ciWorkflow, "Run public safety scan") || !strings.Contains(ciWorkflow, "scripts/public_safety_scan.py --root .") {
		t.Fatalf("CI workflow does not run public safety scan:\n%s", ciWorkflow)
	}
	for _, want := range []string{
		"huggingface_hub>=0.23",
		"pyarrow>=15",
		"--suite context-quality",
		"--suite repobench-r",
		"--min-repobench-snapzip-acc1 0.17",
		"--min-repobench-snapzip-acc3 0.34",
		"--min-repobench-snapzip-acc5 0.59",
		"--min-repobench-snapzip-mrr5 0.298667",
		"--min-repobench-snapzip-ndcg5 0.369709",
		"--max-repobench-snapzip-duplicate-top5-records 0",
		"--max-repobench-snapzip-duplicate-top5-slots 0",
		"--min-repobench-snapzip-acc5-over-bm25 0.06",
		"--min-repobench-snapzip-mrr5-over-bm25 0.03",
		"--min-repobench-snapzip-ndcg5-over-bm25 0.04",
		"--min-repobench-snapzip-acc5-over-jaccard 0.10",
		"--suite repobench-p",
		"--repobench-p-sample-size 50",
		"--min-repobench-p-snapzip-gold-hit5 0.90",
		"--min-repobench-p-snapzip-new-token-coverage5 0.26",
		"--min-repobench-p-snapzip-identifier-hit5 0.95",
		"--min-repobench-p-snapzip-new-token-coverage5-over-bm25 0.00",
	} {
		if !strings.Contains(ciWorkflow, want) {
			t.Fatalf("CI workflow missing public retrieval quality gate fragment %q:\n%s", want, ciWorkflow)
		}
	}

	commandsExtra := readRepoFile(t, repoRoot, "cmd/snapzip/commands_extra.go")
	for _, want := range []string{
		"repobench-p",
		"repobench-r-matrix",
		"repobench-matrix-configs",
		"repobench-matrix-splits",
		"repobench-p-data",
		"repobench-p-sample-size",
		"repobench-live",
		"live-cli-cmd",
		"live-model",
		"live-sample-size",
		"snapzip-rerank-cmd",
		"snapzip-diagnostics",
		"snapzip-search-limit",
		"snapzip-diagnostics-limit",
		"min-repobench-p-snapzip-new-token-coverage5-over-bm25",
		"min-repobench-snapzip-ndcg5-over-bm25",
	} {
		if !strings.Contains(commandsExtra, want) {
			t.Fatalf("snapzip eval wrapper missing benchmark flag %q:\n%s", want, commandsExtra)
		}
	}
}

func TestPublicSafetyScan(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot, "scripts", "public_safety_scan.py")

	cleanDir := t.TempDir()
	writeCLIFile(t, cleanDir, "README.md", "public-safe benchmark notes\n")
	cmd := exec.Command("python3", script, "--root", cleanDir)
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("public safety scan rejected clean fixture: %v\n%s", err, string(output))
	}

	leakyDir := t.TempDir()
	writeCLIFile(t, leakyDir, "notes.md", "local path: "+"/Users/"+"MTEnt/Documents/private\n")
	cmd = exec.Command("python3", script, "--root", leakyDir)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("public safety scan accepted leaky fixture:\n%s", string(output))
	}
	if !strings.Contains(string(output), "local developer Documents path") {
		t.Fatalf("public safety scan reported wrong failure:\n%s", string(output))
	}
}

func runSnapZip(t *testing.T, repoRoot string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/snapzip"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("snapzip %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}

func readRepoFile(t *testing.T, repoRoot, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func writeCLIFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
