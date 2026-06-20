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
	writeCLIFile(t, fixture, "lib/cache.rb", "class CacheStore\nend\n")
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
	if !strings.Contains(packOutput, "# SnapZip Context Pack") || !strings.Contains(packOutput, "CacheStore") {
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
	if statsPayload.KnowledgeRows != 3 || statsPayload.FeedbackRows != 0 {
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
}

func TestMCPServerExposesSearchTool(t *testing.T) {
	dbDir := t.TempDir()
	db, err := core.InitDB(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.AddKnowledge(db, "rb", "Source file: lib/cache.rb", "class CacheStore\nend\n"); err != nil {
		t.Fatal(err)
	}
	if err := core.AddKnowledge(db, "rb", "Source file: test/cache_test.rb", "class CacheStoreTest\nend\n"); err != nil {
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
	}, "\n") + "\n"

	var output bytes.Buffer
	if err := runMCPServer(strings.NewReader(input), &output, dbDir); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("got %d MCP responses, want 6:\n%s", len(lines), output.String())
	}
	if !strings.Contains(lines[1], `"tools"`) || !strings.Contains(lines[1], `"context_pack"`) || !strings.Contains(lines[1], `"repair_pack"`) {
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
}

func TestCLIAdvancedContextCommands(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	fixture := t.TempDir()
	writeCLIFile(t, fixture, "pkg/cache.go", "package cache\n\ntype CacheStore struct{}\n\nfunc NewCacheStore() CacheStore { return CacheStore{} }\n")
	writeCLIFile(t, fixture, "pkg/cache_test.go", "package cache\n\nfunc TestCacheStore() {}\n")

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

	relatedOutput := runSnapZip(t, repoRoot, "related", "--db-dir", dbDir, "--path", "pkg/cache.go", "--limit", "5")
	if !strings.Contains(relatedOutput, "pkg/cache_test.go") {
		t.Fatalf("related output missing test file:\n%s", relatedOutput)
	}

	packOutput := runSnapZip(t, repoRoot, "pack", "--db-dir", dbDir, "--query", "CacheStore", "--mode", "test", "--limit", "3", "--budget", "4096")
	if !strings.Contains(packOutput, "Mode: test") || !strings.Contains(packOutput, "pkg/cache_test.go") {
		t.Fatalf("mode-specific pack output missing expected context:\n%s", packOutput)
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
