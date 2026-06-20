package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		"--query", "ruby CacheStore",
		"--limit", "1",
	)
	if !strings.Contains(searchOutput, "CacheStore") {
		t.Fatalf("search output did not include indexed Ruby fixture:\n%s", searchOutput)
	}

	statsOutput := runSnapZip(t, repoRoot, "stats", "--db-dir", dbDir)
	if !strings.Contains(statsOutput, "knowledge rows: 3") {
		t.Fatalf("stats output did not show three indexed files:\n%s", statsOutput)
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
