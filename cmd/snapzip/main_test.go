package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MTEnt/SnapZip/core"
)

func TestCLIInitSearchStatsAndReset(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
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
	if strings.Contains(workflow, "outputs.report-path") || strings.Contains(workflow, "node20") {
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
