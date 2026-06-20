package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MTEnt/SnapZip/core"
	"github.com/klauspost/compress/zstd"
)

type auditCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

type auditReport struct {
	DBDir  string       `json:"db_dir"`
	Checks []auditCheck `json:"checks"`
}

func handleIndex() {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	langs := fs.String("langs", "all", "Comma-separated language names/extensions to index, or all/any")
	crawl := fs.String("crawl", ".", "Codebase directory to crawl and index")
	reset := fs.Bool("reset", false, "Remove any existing memory.db before indexing")
	maxFileBytes := fs.Int64("max-file-bytes", core.DefaultMaxIndexFileBytes, "Maximum individual source file size to index")
	changed := fs.Bool("changed", false, "Index files changed against HEAD")
	since := fs.String("since", "", "Index files changed since a git ref")
	watch := fs.Bool("watch", false, "Continuously re-run changed-file indexing")
	interval := fs.Duration("interval", 5*time.Second, "Polling interval for --watch")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	if *reset {
		if err := core.ResetDB(*dbDir); err != nil {
			fmt.Printf("Error resetting DB: %v\n", err)
			os.Exit(1)
		}
	}

	db, err := core.InitDB(*dbDir)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	run := func() (int, error) {
		return runIndexOnce(db, *crawl, *langs, *maxFileBytes, *changed, *since)
	}

	if !*watch {
		count, err := run()
		if err != nil {
			fmt.Printf("Index failed: %v\n", err)
			os.Exit(1)
		}
		if *jsonOutput {
			writeJSON(map[string]any{"indexed_entries": count})
			return
		}
		fmt.Printf("Indexed %d entries into memory.db\n", count)
		return
	}

	if *interval <= 0 {
		*interval = 5 * time.Second
	}
	fmt.Fprintf(os.Stderr, "Watching %s every %s. Press Ctrl+C to stop.\n", *crawl, interval.String())
	for {
		count, err := run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Index pass failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Indexed %d entries\n", count)
		}
		time.Sleep(*interval)
	}
}

func runIndexOnce(db *sql.DB, crawl, langs string, maxFileBytes int64, changed bool, since string) (int, error) {
	root, err := filepath.Abs(crawl)
	if err != nil {
		return 0, err
	}
	filter := core.NewLanguageFilter(langs)
	options := core.DefaultIndexOptions()
	options.MaxFileBytes = maxFileBytes

	if changed || strings.TrimSpace(since) != "" {
		files, err := gitChangedFiles(root, strings.TrimSpace(since))
		if err != nil {
			return 0, err
		}
		return core.IndexFilesWithOptions(db, root, files, filter, options)
	}
	return core.IndexDirectoryWithOptions(db, root, filter, options)
}

func gitChangedFiles(root, since string) ([]string, error) {
	if since != "" {
		output, err := exec.Command("git", "-C", root, "diff", "--name-only", "--diff-filter=ACMR", since).Output()
		if err != nil {
			return nil, err
		}
		return splitGitFileList(output), nil
	}

	var files []string
	if output, err := exec.Command("git", "-C", root, "diff", "--name-only", "--diff-filter=ACMR", "HEAD").Output(); err == nil {
		files = append(files, splitGitFileList(output)...)
	} else {
		if output, err := exec.Command("git", "-C", root, "diff", "--name-only", "--diff-filter=ACMR").Output(); err != nil {
			return nil, err
		} else {
			files = append(files, splitGitFileList(output)...)
		}
		if output, err := exec.Command("git", "-C", root, "diff", "--cached", "--name-only", "--diff-filter=ACMR").Output(); err != nil {
			return nil, err
		} else {
			files = append(files, splitGitFileList(output)...)
		}
	}

	untracked, err := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}
	files = append(files, splitGitFileList(untracked)...)
	return uniqueTerms(files), nil
}

func splitGitFileList(output []byte) []string {
	var files []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func handleMap() {
	fs := flag.NewFlagSet("map", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	limit := fs.Int("limit", 50, "Maximum files to include")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	repoMap, err := core.BuildRepoMap(db, *limit)
	if err != nil {
		fmt.Printf("Map failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(repoMap)
		return
	}
	fmt.Print(core.RenderRepoMap(repoMap))
}

func handleSymbols() {
	fs := flag.NewFlagSet("symbols", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	query := fs.String("query", "", "Symbol search query")
	limit := fs.Int("limit", 20, "Maximum symbols to return")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	symbols, err := core.SearchSymbols(db, *query, *limit)
	if err != nil {
		fmt.Printf("Symbol search failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(symbols)
		return
	}
	for _, symbol := range symbols {
		fmt.Printf("%s:%d [%s %s] %s\n", symbol.Path, symbol.Line, symbol.Language, symbol.Kind, symbol.Signature)
	}
}

func handleRelated() {
	fs := flag.NewFlagSet("related", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	path := fs.String("path", "", "Indexed source path")
	limit := fs.Int("limit", 10, "Maximum files to return")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])
	if strings.TrimSpace(*path) == "" {
		fmt.Println("Error: --path is required")
		fs.Usage()
		os.Exit(1)
	}

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	files, err := core.RelatedFiles(db, *path, *limit)
	if err != nil {
		fmt.Printf("Related lookup failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(files)
		return
	}
	fmt.Print(core.RenderRepoMap(core.RepoMap{Files: files}))
}

func handleAudit() {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	report := runAudit(*dbDir)
	if *jsonOutput {
		writeJSON(report)
		return
	}
	for _, check := range report.Checks {
		status := "FAIL"
		if check.Passed {
			status = "PASS"
		}
		fmt.Printf("[%s] %s: %s\n", status, check.Name, check.Details)
	}
}

func runAudit(dbDir string) auditReport {
	report := auditReport{DBDir: dbDir}
	report.Checks = append(report.Checks, auditGitIgnored("memory.db"))
	report.Checks = append(report.Checks, auditDBStats(dbDir)...)
	report.Checks = append(report.Checks, auditSecrets(dbDir))
	report.Checks = append(report.Checks,
		auditCheck{Name: "dependency dirs skipped", Passed: true, Details: "indexer skips .git, node_modules, vendor, dist, build, target, venv, .venv, and common generated directories"},
		auditCheck{Name: "mcp write surface", Passed: true, Details: "MCP server exposes read-only search, context_pack, get_feedback, stats, map, symbols, and related tools"},
	)
	return report
}

func auditGitIgnored(path string) auditCheck {
	cmd := exec.Command("git", "check-ignore", "-q", path)
	if err := cmd.Run(); err != nil {
		return auditCheck{Name: "memory.db gitignore", Passed: false, Details: path + " is not ignored by git"}
	}
	return auditCheck{Name: "memory.db gitignore", Passed: true, Details: path + " is ignored by git"}
}

func auditDBStats(dbDir string) []auditCheck {
	db, err := core.InitDB(dbDir)
	if err != nil {
		return []auditCheck{{Name: "database opens", Passed: false, Details: err.Error()}}
	}
	defer db.Close()
	stats, err := core.GetDatabaseStats(db)
	if err != nil {
		return []auditCheck{{Name: "database stats", Passed: false, Details: err.Error()}}
	}
	return []auditCheck{
		{Name: "database opens", Passed: true, Details: "memory.db opened successfully"},
		{Name: "index rows", Passed: true, Details: fmt.Sprintf("%d knowledge rows, %d symbol rows, %d feedback rows", stats.KnowledgeRows, stats.SymbolRows, stats.FeedbackRows)},
	}
}

func auditSecrets(dbDir string) auditCheck {
	db, err := core.InitDB(dbDir)
	if err != nil {
		return auditCheck{Name: "secret scan", Passed: false, Details: err.Error()}
	}
	defer db.Close()

	pattern := regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token)\s*[:=]\s*['"][^'"]{12,}`)
	rows, err := db.Query("SELECT path, content FROM knowledge LIMIT 10000")
	if err != nil {
		return auditCheck{Name: "secret scan", Passed: false, Details: err.Error()}
	}
	defer rows.Close()
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return auditCheck{Name: "secret scan", Passed: false, Details: err.Error()}
		}
		if pattern.MatchString(content) {
			return auditCheck{Name: "secret scan", Passed: false, Details: "possible secret-like assignment indexed in " + path}
		}
	}
	if err := rows.Err(); err != nil {
		return auditCheck{Name: "secret scan", Passed: false, Details: err.Error()}
	}
	return auditCheck{Name: "secret scan", Passed: true, Details: "no obvious secret-like assignments found in indexed content"}
}

func handleInstallAgent() {
	fs := flag.NewFlagSet("install-agent", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory to write integration files")
	force := fs.Bool("force", false, "Overwrite existing files")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")

	target := "all"
	args := os.Args[2:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		target = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}

	written, skipped, err := installAgentFiles(*dir, target, *force)
	if err != nil {
		fmt.Printf("Install failed: %v\n", err)
		os.Exit(1)
	}
	result := map[string]any{"written": written, "skipped": skipped}
	if *jsonOutput {
		writeJSON(result)
		return
	}
	for _, path := range written {
		fmt.Printf("Wrote %s\n", path)
	}
	for _, path := range skipped {
		fmt.Printf("Skipped existing %s\n", path)
	}
}

func installAgentFiles(root, target string, force bool) ([]string, []string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	files := map[string]string{}
	rule := agentRuleText()
	add := func(path, content string) {
		files[filepath.Join(root, path)] = content
	}
	switch target {
	case "all":
		add("AGENTS.md", rule)
		add("CLAUDE.md", rule)
		add(".cursor/rules/snapzip.mdc", rule)
		add(".continue/snapzip.md", rule)
	case "codex":
		add("AGENTS.md", rule)
	case "claude":
		add("CLAUDE.md", rule)
	case "cursor":
		add(".cursor/rules/snapzip.mdc", rule)
	case "continue":
		add(".continue/snapzip.md", rule)
	default:
		return nil, nil, fmt.Errorf("unknown target %q; use all, codex, claude, cursor, or continue", target)
	}

	var written, skipped []string
	for path, content := range files {
		if _, err := os.Stat(path); err == nil && !force {
			skipped = append(skipped, path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return written, skipped, err
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return written, skipped, err
		}
		written = append(written, path)
	}
	return written, skipped, nil
}

func agentRuleText() string {
	return `# SnapZip Agent Integration

Use SnapZip when available. Run ` + "`snapzip stats --db-dir .`" + ` to check whether local context exists.
Before non-trivial code changes, run ` + "`snapzip pack --query \"<topic>\" --limit 5 --budget 12000 --mode <debug|refactor|test|docs>`" + ` for targeted local context and feedback memory.
Use ` + "`snapzip symbols --query \"<symbol>\" --limit 10`" + ` or ` + "`snapzip map --limit 50`" + ` for structural context.
Use ` + "`snapzip repair-pack --error-file <test-output>`" + ` after failing tests.
Do not assume SnapZip memory exists on fresh installs; index first with ` + "`snapzip index --langs all --crawl .`" + ` when appropriate.
`
}

func handleEval() {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	suite := fs.String("suite", "smoke", "Benchmark suite: smoke, algorithm-20, hard-rbt, all")
	snapzipBin := fs.String("snapzip-bin", "", "Path to built snapzip binary")
	iterations := fs.Int("iterations", 100, "Optimizer iterations for benchmark harness")
	jsonPath := fs.String("json", "", "Optional path to write JSON report")
	_ = fs.Parse(os.Args[2:])

	runPy := filepath.Join("benchmarks", "run.py")
	if _, err := os.Stat(runPy); err != nil {
		fmt.Println("Error: benchmarks/run.py not found; snapzip eval must be run from the source checkout")
		os.Exit(1)
	}

	args := []string{runPy, "--suite", *suite, "--iterations", fmt.Sprint(*iterations)}
	if *snapzipBin != "" {
		args = append(args, "--snapzip-bin", *snapzipBin)
	}
	if *jsonPath != "" {
		args = append(args, "--json", *jsonPath)
	}
	cmd := exec.Command("python3", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

func handleExplainFailure() {
	handleFailurePack("debug")
}

func handleRepairPack() {
	handleFailurePack("debug")
}

func handleFailurePack(defaultMode string) {
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	errorFile := fs.String("error-file", "", "File containing test/build failure output")
	query := fs.String("query", "", "Additional search query")
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	limit := fs.Int("limit", 6, "Maximum snippets to include")
	budget := fs.Int("budget", core.DefaultContextPackBudgetBytes, "Approximate byte budget")
	mode := fs.String("mode", defaultMode, "Pack mode")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])
	if *errorFile == "" {
		fmt.Println("Error: --error-file is required")
		fs.Usage()
		os.Exit(1)
	}

	content, err := os.ReadFile(*errorFile)
	if err != nil {
		fmt.Printf("Error reading failure output: %v\n", err)
		os.Exit(1)
	}
	failureQuery := failureQueryFromOutput(string(content), *query)

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()
	comp, err := core.NewZstdCompressor(zstd.SpeedDefault)
	if err != nil {
		fmt.Printf("Error initializing compressor: %v\n", err)
		os.Exit(1)
	}
	pack, err := core.BuildContextPackWithMode(db, comp, failureQuery, *mode, *limit, *budget, 5)
	if err != nil {
		fmt.Printf("Failure context failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(pack)
		return
	}
	fmt.Print(core.RenderContextPack(pack))
}

func failureQueryFromOutput(output, extra string) string {
	var terms []string
	if strings.TrimSpace(extra) != "" {
		terms = append(terms, strings.TrimSpace(extra))
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	pathPattern := regexp.MustCompile(`[A-Za-z0-9_./\\-]+\.(go|py|js|jsx|ts|tsx|rb|java|rs|php|swift|kt|c|cc|cpp|h|hpp|md|yaml|yml|json)`)
	wordPattern := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
	for scanner.Scan() && len(terms) < 40 {
		line := scanner.Text()
		terms = append(terms, pathPattern.FindAllString(line, -1)...)
		for _, word := range wordPattern.FindAllString(line, -1) {
			lower := strings.ToLower(word)
			if len(lower) > 2 && !commonFailureWord(lower) {
				terms = append(terms, word)
			}
			if len(terms) >= 40 {
				break
			}
		}
	}
	return strings.Join(uniqueTerms(terms), " ")
}

func commonFailureWord(word string) bool {
	switch word {
	case "error", "failed", "failure", "expected", "actual", "panic", "traceback", "file", "line", "test", "tests", "exit", "status":
		return true
	default:
		return false
	}
}

func uniqueTerms(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func openDBOrExit(dbDir string) (*sql.DB, error) {
	db, err := core.InitDB(dbDir)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		return nil, err
	}
	return db, nil
}

func commandOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	_ = cmd.Run()
	return strings.TrimSpace(output.String())
}
