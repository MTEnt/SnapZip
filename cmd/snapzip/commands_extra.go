package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	maxContentBytes := fs.Int("max-content-bytes", core.DefaultMaxKnowledgeContentBytes, "Maximum source bytes per indexed snippet")
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
		return runIndexOnce(db, *crawl, *langs, *maxFileBytes, *maxContentBytes, *changed, *since)
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

func runIndexOnce(db *sql.DB, crawl, langs string, maxFileBytes int64, maxContentBytes int, changed bool, since string) (int, error) {
	root, err := filepath.Abs(crawl)
	if err != nil {
		return 0, err
	}
	filter := core.NewLanguageFilter(langs)
	options := core.DefaultIndexOptions()
	options.MaxFileBytes = maxFileBytes
	options.MaxContentBytes = maxContentBytes

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
		return filterGeneratedChangedFiles(splitGitFileList(output)), nil
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
	return filterGeneratedChangedFiles(files), nil
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

func filterGeneratedChangedFiles(files []string) []string {
	var filtered []string
	for _, file := range uniqueTerms(files) {
		normalized := filepath.ToSlash(strings.TrimSpace(file))
		base := strings.ToLower(filepath.Base(normalized))
		switch base {
		case "memory.db", "memory.db-shm", "memory.db-wal", ".snapzip-pr-context.md", ".snapzip-pr-context.json":
			continue
		}
		filtered = append(filtered, normalized)
	}
	return filtered
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

func handleSymbolContext() {
	fs := flag.NewFlagSet("symbol-context", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	query := fs.String("query", "", "Symbol, function, class, method, or call-site query")
	limit := fs.Int("limit", 20, "Maximum definitions and references to return")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])
	if strings.TrimSpace(*query) == "" {
		fmt.Println("Error: --query is required")
		fs.Usage()
		os.Exit(1)
	}

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	context, err := core.BuildSymbolContext(db, *query, *limit)
	if err != nil {
		fmt.Printf("Symbol context failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(context)
		return
	}
	fmt.Print(core.RenderSymbolContext(context))
}

func handleImports() {
	fs := flag.NewFlagSet("imports", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	query := fs.String("query", "", "Import path, module, package, file, or dependency query")
	limit := fs.Int("limit", 20, "Maximum imports to return")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])
	if strings.TrimSpace(*query) == "" {
		fmt.Println("Error: --query is required")
		fs.Usage()
		os.Exit(1)
	}

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	context, err := core.BuildImportContext(db, *query, *limit)
	if err != nil {
		fmt.Printf("Import lookup failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(context)
		return
	}
	fmt.Print(core.RenderImportContext(context))
}

func handleGraph() {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	path := fs.String("path", "", "Indexed source path")
	limit := fs.Int("limit", 20, "Maximum outgoing and incoming imports to return")
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

	graph, err := core.BuildDependencyGraph(db, *path, *limit)
	if err != nil {
		fmt.Printf("Graph lookup failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(graph)
		return
	}
	fmt.Print(core.RenderDependencyGraph(graph))
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

func handleAffected() {
	fs := flag.NewFlagSet("affected", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	pathInput := fs.String("path", "", "Comma-separated indexed source paths")
	changed := fs.Bool("changed", false, "Use git changed files")
	since := fs.String("since", "", "Use files changed since a git ref")
	dir := fs.String("dir", ".", "Git working directory for --changed or --since")
	limit := fs.Int("limit", 10, "Maximum tests and related files to return")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	paths, err := resolveAffectedPaths(*pathInput, *changed, *since, *dir)
	if err != nil {
		fmt.Printf("Affected lookup failed: %v\n", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Println("Error: provide --path or --changed/--since")
		fs.Usage()
		os.Exit(1)
	}

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	report, err := core.FindAffectedTests(db, paths, *limit)
	if err != nil {
		fmt.Printf("Affected lookup failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(report)
		return
	}
	fmt.Print(renderAffectedReport(report))
}

type validateCommandResult struct {
	Command  string `json:"command"`
	Dir      string `json:"dir"`
	ExitCode int    `json:"exit_code"`
	Passed   bool   `json:"passed"`
	Output   string `json:"output"`
}

type validateReport struct {
	Status string                 `json:"status"`
	Dir    string                 `json:"dir"`
	Plan   core.ValidationPlan    `json:"plan"`
	Run    *validateCommandResult `json:"run,omitempty"`
	Pack   *core.ContextPack      `json:"pack,omitempty"`
}

type prReport struct {
	Base         string              `json:"base,omitempty"`
	Dir          string              `json:"dir"`
	ChangedFiles []string            `json:"changed_files"`
	Plan         core.ValidationPlan `json:"plan"`
	Pack         *core.ContextPack   `json:"pack,omitempty"`
}

func handleValidate() {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	pathInput := fs.String("path", "", "Comma-separated indexed source paths")
	changed := fs.Bool("changed", false, "Use git changed files")
	since := fs.String("since", "", "Use files changed since a git ref")
	dir := fs.String("dir", ".", "Git and command working directory")
	commandText := fs.String("cmd", "", "Optional validation command to run, such as 'go test ./...'")
	runConfig := fs.Bool("run-config", false, "Run the configured project validation command when --cmd is not provided")
	query := fs.String("query", "", "Additional repair-pack search query when validation fails")
	limit := fs.Int("limit", 10, "Maximum tests and related files to include")
	budget := fs.Int("budget", core.DefaultContextPackBudgetBytes, "Approximate repair-pack byte budget")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])
	if strings.TrimSpace(*commandText) == "" && fs.NArg() > 0 {
		*commandText = strings.Join(fs.Args(), " ")
	}

	paths, err := resolveAffectedPaths(*pathInput, *changed, *since, *dir)
	if err != nil {
		fmt.Printf("Validate failed: %v\n", err)
		os.Exit(1)
	}
	if len(paths) == 0 && strings.TrimSpace(*commandText) == "" {
		fmt.Println("Error: provide --path, --changed/--since, or --cmd")
		fs.Usage()
		os.Exit(1)
	}

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	plan, err := core.BuildValidationPlan(db, paths, *limit)
	if err != nil {
		fmt.Printf("Validate failed: %v\n", err)
		os.Exit(1)
	}
	config, err := core.LoadProjectConfig(*dir)
	if err != nil {
		fmt.Printf("Validate failed: %v\n", err)
		os.Exit(1)
	}
	configuredCommands := core.ConfiguredValidationCommands(config, plan.Affected)
	plan.SuggestedCommands = core.MergeValidationCommands(configuredCommands, plan.SuggestedCommands)
	if *runConfig && strings.TrimSpace(*commandText) == "" {
		if len(configuredCommands) == 0 {
			fmt.Println("Error: --run-config was provided, but no validation command is configured")
			os.Exit(1)
		}
		*commandText = configuredCommands[0].Command
	}
	report := validateReport{
		Status: "planned",
		Dir:    *dir,
		Plan:   plan,
	}

	if strings.TrimSpace(*commandText) != "" {
		output, exitCode := runDiagnoseCommand(*commandText, *dir)
		report.Run = &validateCommandResult{
			Command:  *commandText,
			Dir:      *dir,
			ExitCode: exitCode,
			Passed:   exitCode == 0,
			Output:   output,
		}
		if exitCode == 0 {
			report.Status = "passed"
		} else {
			report.Status = "failed"
			comp, err := core.NewZstdCompressor(zstd.SpeedDefault)
			if err != nil {
				fmt.Printf("Error initializing compressor: %v\n", err)
				os.Exit(1)
			}
			extraQuery := strings.TrimSpace(strings.Join(append([]string{*query}, plan.InputPaths...), " "))
			pack, err := core.BuildRepairContextPack(db, comp, output, extraQuery, "debug", *limit, *budget, 5)
			if err != nil {
				fmt.Printf("Validate failed: %v\n", err)
				os.Exit(1)
			}
			report.Pack = &pack
		}
	}

	if *jsonOutput {
		writeJSON(report)
	} else {
		fmt.Print(renderValidateReport(report))
	}
	if report.Run != nil && report.Run.ExitCode != 0 {
		os.Exit(report.Run.ExitCode)
	}
}

func handlePR() {
	fs := flag.NewFlagSet("pr", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	dir := fs.String("dir", ".", "Git and config working directory")
	pathInput := fs.String("path", "", "Comma-separated indexed source paths to review")
	changed := fs.Bool("changed", false, "Use git working-tree changed files")
	base := fs.String("base", "", "Git base ref for a PR diff, such as origin/main")
	query := fs.String("query", "", "Additional review context query")
	limit := fs.Int("limit", 10, "Maximum affected files and context snippets to include")
	budget := fs.Int("budget", core.DefaultContextPackBudgetBytes, "Approximate byte budget for review context")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	if strings.TrimSpace(*pathInput) == "" && !*changed && strings.TrimSpace(*base) == "" {
		fmt.Println("Error: provide --path, --changed, or --base")
		fs.Usage()
		os.Exit(1)
	}

	db, err := openDBOrExit(*dbDir)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	report, err := buildPRReport(db, *dir, *pathInput, *changed, *base, *query, *limit, *budget)
	if err != nil {
		fmt.Printf("PR context failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOutput {
		writeJSON(report)
		return
	}
	fmt.Print(renderPRReport(report))
}

func buildPRReport(db *sql.DB, dir, pathInput string, changed bool, base, query string, limit, budget int) (prReport, error) {
	paths, err := resolveAffectedPaths(pathInput, changed, base, dir)
	if err != nil {
		return prReport{}, err
	}
	report := prReport{
		Base:         strings.TrimSpace(base),
		Dir:          dir,
		ChangedFiles: paths,
	}
	if changed && report.Base == "" {
		report.Base = "working tree"
	}
	if len(paths) == 0 {
		return report, nil
	}

	plan, err := core.BuildValidationPlan(db, paths, limit)
	if err != nil {
		return prReport{}, err
	}
	config, err := core.LoadProjectConfig(dir)
	if err != nil {
		return prReport{}, err
	}
	plan.SuggestedCommands = core.MergeValidationCommands(core.ConfiguredValidationCommands(config, plan.Affected), plan.SuggestedCommands)
	report.Plan = plan

	comp, err := core.NewZstdCompressor(zstd.SpeedDefault)
	if err != nil {
		return prReport{}, err
	}
	packQuery := prContextQuery(query, paths, plan.Affected)
	if strings.TrimSpace(packQuery) != "" {
		pack, err := core.BuildContextPackWithMode(db, comp, packQuery, "review", limit, budget, 5)
		if err != nil {
			return prReport{}, err
		}
		report.Pack = &pack
	}
	return report, nil
}

func prContextQuery(query string, paths []string, affected core.AffectedReport) string {
	terms := []string{strings.TrimSpace(query), "code review diff regression risk validation"}
	addPathTerms := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		base := filepath.Base(path)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		terms = append(terms, path, base, stem)
	}
	for _, path := range paths {
		addPathTerms(path)
	}
	for _, test := range affected.Tests {
		addPathTerms(test.Path)
	}
	for _, related := range affected.Related {
		addPathTerms(related.Path)
	}
	return strings.Join(uniqueTerms(terms), " ")
}

func renderPRReport(report prReport) string {
	var builder strings.Builder
	builder.WriteString("# SnapZip PR Context\n\n")
	if report.Base != "" {
		fmt.Fprintf(&builder, "Base: `%s`\n", report.Base)
	}
	if report.Dir != "" {
		fmt.Fprintf(&builder, "Directory: `%s`\n", report.Dir)
	}

	builder.WriteString("\n## Changed Files\n")
	if len(report.ChangedFiles) == 0 {
		builder.WriteString("\nNo changed files found.\n")
		return builder.String()
	}
	for _, path := range report.ChangedFiles {
		fmt.Fprintf(&builder, "- %s\n", path)
	}

	builder.WriteString("\n## Validation Plan\n")
	renderAffectedReportBody(&builder, report.Plan.Affected)
	builder.WriteString("\n## Suggested Commands\n")
	if len(report.Plan.SuggestedCommands) == 0 {
		builder.WriteString("\nNo validation command could be inferred from the current index.\n")
	} else {
		for _, command := range report.Plan.SuggestedCommands {
			fmt.Fprintf(&builder, "\n- `%s` (confidence %.2f)\n", command.Command, command.Confidence)
			if command.Reason != "" {
				fmt.Fprintf(&builder, "  - %s\n", command.Reason)
			}
		}
	}

	if report.Pack != nil {
		builder.WriteString("\n## Review Context\n\n")
		builder.WriteString(core.RenderContextPack(*report.Pack))
	}
	return builder.String()
}

type diagnoseReport struct {
	Command  string            `json:"command"`
	Dir      string            `json:"dir"`
	ExitCode int               `json:"exit_code"`
	Passed   bool              `json:"passed"`
	Output   string            `json:"output"`
	Pack     *core.ContextPack `json:"pack,omitempty"`
}

func handleDiagnose() {
	fs := flag.NewFlagSet("diagnose", flag.ExitOnError)
	commandText := fs.String("cmd", "", "Command to run, such as 'go test ./...'")
	dir := fs.String("dir", ".", "Working directory for the command")
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	query := fs.String("query", "", "Additional repair-pack search query")
	limit := fs.Int("limit", 6, "Maximum repair snippets to include")
	budget := fs.Int("budget", core.DefaultContextPackBudgetBytes, "Approximate byte budget")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	alwaysPack := fs.Bool("always-pack", false, "Build a pack even when the command succeeds")
	_ = fs.Parse(os.Args[2:])
	if strings.TrimSpace(*commandText) == "" && fs.NArg() > 0 {
		*commandText = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(*commandText) == "" {
		fmt.Println("Error: provide --cmd or a command after flags")
		fs.Usage()
		os.Exit(1)
	}

	output, exitCode := runDiagnoseCommand(*commandText, *dir)
	report := diagnoseReport{
		Command:  *commandText,
		Dir:      *dir,
		ExitCode: exitCode,
		Passed:   exitCode == 0,
		Output:   output,
	}

	if exitCode != 0 || *alwaysPack {
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
		pack, err := core.BuildRepairContextPack(db, comp, output, *query, "debug", *limit, *budget, 5)
		if err != nil {
			fmt.Printf("Diagnose failed: %v\n", err)
			os.Exit(1)
		}
		report.Pack = &pack
	}

	if *jsonOutput {
		writeJSON(report)
		return
	}
	fmt.Printf("# SnapZip Diagnose\n\nCommand: `%s`\nExit code: %d\n", report.Command, report.ExitCode)
	if report.Passed {
		fmt.Println("Status: passed")
	} else {
		fmt.Println("Status: failed")
	}
	if report.Pack != nil {
		fmt.Print("\n")
		fmt.Print(core.RenderContextPack(*report.Pack))
	} else {
		fmt.Println("\nNo repair pack built because the command passed.")
	}
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

func renderAffectedReport(report core.AffectedReport) string {
	var builder strings.Builder
	builder.WriteString("# SnapZip Affected Tests\n\n")
	renderAffectedReportBody(&builder, report)
	return builder.String()
}

func renderAffectedReportBody(builder *strings.Builder, report core.AffectedReport) {
	if len(report.InputPaths) > 0 {
		builder.WriteString("Input paths:\n")
		for _, path := range report.InputPaths {
			fmt.Fprintf(builder, "- %s\n", path)
		}
	}
	builder.WriteString("\n## Likely Tests\n")
	if len(report.Tests) == 0 {
		builder.WriteString("\nNo likely tests found in the current index.\n")
	} else {
		for _, test := range report.Tests {
			fmt.Fprintf(builder, "\n- %s", test.Path)
			if test.Confidence > 0 {
				fmt.Fprintf(builder, " (confidence %.2f)", test.Confidence)
			}
			builder.WriteByte('\n')
			for _, reason := range test.Reasons {
				fmt.Fprintf(builder, "  - %s\n", reason)
			}
		}
	}
	if len(report.Related) > 0 {
		builder.WriteString("\n## Related Files\n")
		for _, file := range report.Related {
			fmt.Fprintf(builder, "\n- %s", file.Path)
			if file.Confidence > 0 {
				fmt.Fprintf(builder, " (confidence %.2f)", file.Confidence)
			}
			builder.WriteByte('\n')
			for _, reason := range file.Reasons {
				fmt.Fprintf(builder, "  - %s\n", reason)
			}
		}
	}
}

func renderValidateReport(report validateReport) string {
	var builder strings.Builder
	builder.WriteString("# SnapZip Validate\n\n")
	fmt.Fprintf(&builder, "Status: %s\n", report.Status)
	if report.Dir != "" {
		fmt.Fprintf(&builder, "Directory: `%s`\n", report.Dir)
	}

	builder.WriteString("\n## Validation Plan\n")
	renderAffectedReportBody(&builder, report.Plan.Affected)
	builder.WriteString("\n## Suggested Commands\n")
	if len(report.Plan.SuggestedCommands) == 0 {
		builder.WriteString("\nNo validation command could be inferred from the current index.\n")
	} else {
		for _, command := range report.Plan.SuggestedCommands {
			fmt.Fprintf(&builder, "\n- `%s` (confidence %.2f)\n", command.Command, command.Confidence)
			if command.Reason != "" {
				fmt.Fprintf(&builder, "  - %s\n", command.Reason)
			}
		}
	}

	if report.Run != nil {
		builder.WriteString("\n## Command Result\n\n")
		fmt.Fprintf(&builder, "Command: `%s`\n", report.Run.Command)
		fmt.Fprintf(&builder, "Exit code: %d\n", report.Run.ExitCode)
		if report.Run.Passed {
			builder.WriteString("Status: passed\n")
		} else {
			builder.WriteString("Status: failed\n")
		}
	}
	if report.Pack != nil {
		builder.WriteString("\n## Repair Context\n\n")
		builder.WriteString(core.RenderContextPack(*report.Pack))
	}
	return builder.String()
}

func resolveAffectedPaths(pathInput string, changed bool, since, dir string) ([]string, error) {
	var paths []string
	if strings.TrimSpace(pathInput) != "" {
		paths = append(paths, strings.Split(pathInput, ",")...)
	}
	if changed || strings.TrimSpace(since) != "" {
		root, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		changedFiles, err := gitChangedFiles(root, strings.TrimSpace(since))
		if err != nil {
			return nil, err
		}
		paths = append(paths, changedFiles...)
	}
	return uniqueTerms(paths), nil
}

func runDiagnoseCommand(commandText, dir string) (string, int) {
	cmd := exec.Command("sh", "-c", commandText)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err == nil {
		return output.String(), 0
	}
	if cmd.ProcessState != nil {
		return output.String(), cmd.ProcessState.ExitCode()
	}
	return output.String() + err.Error(), 1
}

func runAudit(dbDir string) auditReport {
	report := auditReport{DBDir: dbDir}
	report.Checks = append(report.Checks, auditGitIgnored("memory.db"))
	report.Checks = append(report.Checks, auditSnapZipIgnore("."))
	report.Checks = append(report.Checks, auditProjectConfig("."))
	report.Checks = append(report.Checks, auditDBStats(dbDir)...)
	report.Checks = append(report.Checks, auditSecrets(dbDir))
	report.Checks = append(report.Checks,
		auditCheck{Name: "dependency dirs skipped", Passed: true, Details: "indexer skips .git, node_modules, vendor, dist, build, target, venv, .venv, and common generated directories"},
		auditCheck{Name: "mcp write surface", Passed: true, Details: "MCP server exposes read-only search, context_pack, repair_pack, affected_tests, validation_plan, pr_context, get_feedback, stats, map, symbols, symbol_context, imports, graph, and related tools"},
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

func auditSnapZipIgnore(root string) auditCheck {
	patterns, err := core.LoadSnapZipIgnore(root)
	if err != nil {
		return auditCheck{Name: ".snapzipignore", Passed: false, Details: err.Error()}
	}
	if len(patterns) == 0 {
		return auditCheck{Name: ".snapzipignore", Passed: true, Details: "optional .snapzipignore not present; default dependency/generated skips still apply"}
	}
	return auditCheck{Name: ".snapzipignore", Passed: true, Details: fmt.Sprintf("%d local ignore patterns loaded", len(patterns))}
}

func auditProjectConfig(root string) auditCheck {
	config, err := core.LoadProjectConfig(root)
	if err != nil {
		return auditCheck{Name: ".snapzip/config.toml", Passed: false, Details: err.Error()}
	}
	if !config.Found {
		return auditCheck{Name: ".snapzip/config.toml", Passed: true, Details: "optional project profile not present"}
	}
	configuredCount := len(core.ConfiguredValidationCommands(config, core.AffectedReport{}))
	configuredCount += len(config.Validation.Commands)
	return auditCheck{Name: ".snapzip/config.toml", Passed: true, Details: fmt.Sprintf("project profile loaded with %d validation command entries", configuredCount)}
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
		{Name: "index rows", Passed: true, Details: fmt.Sprintf("%d knowledge rows, %d symbol rows, %d symbol reference rows, %d import rows, %d feedback rows", stats.KnowledgeRows, stats.SymbolRows, stats.SymbolReferenceRows, stats.ImportRows, stats.FeedbackRows)},
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

func handleInitConfig() {
	fs := flag.NewFlagSet("init-config", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	force := fs.Bool("force", false, "Overwrite existing .snapzip/config.toml")
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	path, written, err := core.WriteDefaultProjectConfig(*dir, *force)
	if err != nil {
		fmt.Printf("Config init failed: %v\n", err)
		os.Exit(1)
	}
	result := map[string]any{"path": path, "written": written}
	if *jsonOutput {
		writeJSON(result)
		return
	}
	if written {
		fmt.Printf("Wrote %s\n", path)
	} else {
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
	case "mcp":
		mcpPath, err := installMCPGlobal()
		if err != nil {
			return nil, nil, err
		}
		return []string{mcpPath}, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown target %q; use all, codex, claude, cursor, continue, or mcp", target)
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
Before non-trivial code changes, run ` + "`snapzip pack --query \"<topic>\" --limit 5 --budget 12000 --mode <debug|refactor|test|docs|review>`" + ` for targeted local context, receipts, quality warnings, and feedback memory.
Use ` + "`snapzip symbol-context --query \"<symbol>\" --limit 10`" + `, ` + "`snapzip symbols --query \"<symbol>\" --limit 10`" + `, ` + "`snapzip imports --query \"<module>\" --limit 10`" + `, ` + "`snapzip graph --path <file> --limit 10`" + `, or ` + "`snapzip map --limit 50`" + ` for structural context.
Prefer resolved local import targets when present; unresolved imports are usually external packages or aliases SnapZip cannot map safely.
Use ` + "`snapzip related --path <file>`" + ` and ` + "`snapzip affected --path <file>`" + ` to find related files and likely tests.
Use ` + "`snapzip pr --changed`" + ` or ` + "`snapzip pr --base <ref>`" + ` for diff-aware review context before reviewing or finalizing a branch.
Use ` + "`snapzip validate --path <file>`" + ` to plan validation, or ` + "`snapzip validate --changed --cmd \"<test command>\"`" + ` to run validation and get failure context.
If ` + "`.snapzip/config.toml`" + ` defines validation, use ` + "`snapzip validate --changed --run-config`" + ` when explicitly running the configured project command.
Use ` + "`snapzip repair-pack --error-file <test-output>`" + ` or ` + "`snapzip diagnose --cmd \"<test command>\"`" + ` after failing tests.
Do not assume SnapZip memory exists on fresh installs; index first with ` + "`snapzip index --langs all --crawl .`" + ` when appropriate.
`
}

func handleEval() {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	suite := fs.String("suite", "smoke", "Benchmark suite: smoke, algorithm-20, hard-rbt, repair-retrieval, context-quality, repobench-r, repobench-r-matrix, repobench-p, repobench-live, all")
	snapzipBin := fs.String("snapzip-bin", "", "Path to built snapzip binary")
	iterations := fs.Int("iterations", 100, "Optimizer iterations for benchmark harness")
	jsonPath := fs.String("json", "", "Optional path to write JSON report")
	keepWorkdir := fs.String("keep-workdir", "", "Optional directory to keep generated benchmark files")
	repobenchData := fs.String("repobench-data", "", "Path to a RepoBench-R data/<config>.gz file or data directory")
	repobenchConfig := fs.String("repobench-config", "python_cff", "RepoBench-R config")
	repobenchSplit := fs.String("repobench-split", "hard", "RepoBench-R split: easy or hard")
	repobenchSampleSize := fs.Int("repobench-sample-size", 100, "RepoBench-R sample size; use 0 for the full split")
	repobenchSeed := fs.Int("repobench-seed", 42, "RepoBench-R sample seed")
	repobenchMatrixConfigs := fs.String("repobench-matrix-configs", "python_cff,python_cfr,java_cff,java_cfr", "Comma-separated RepoBench-R configs for matrix mode")
	repobenchMatrixSplits := fs.String("repobench-matrix-splits", "easy,hard", "Comma-separated RepoBench-R splits for matrix mode")
	repobenchPData := fs.String("repobench-p-data", "", "Path to a RepoBench v1.1 parquet file or split directory")
	repobenchPLanguage := fs.String("repobench-p-language", "python", "RepoBench v1.1 language: python or java")
	repobenchPSplit := fs.String("repobench-p-split", "cross_file_first", "RepoBench v1.1 split")
	repobenchPSampleSize := fs.Int("repobench-p-sample-size", 100, "RepoBench v1.1 sample size")
	repobenchPSeed := fs.Int("repobench-p-seed", 42, "RepoBench v1.1 sample seed")
	repobenchPMaxShards := fs.Int("repobench-p-max-shards", 1, "Maximum RepoBench v1.1 parquet shards to load from Hugging Face; use 0 for all matching shards")
	snapzipRerankCmd := fs.String("snapzip-rerank-cmd", "", "Command to run external reranker in snapzip search during benchmarks")
	snapzipDiagnostics := fs.Bool("snapzip-diagnostics", false, "Include compact snapzip search score diagnostics in RepoBench records")
	snapzipSearchLimit := fs.Int("snapzip-search-limit", 5, "SnapZip search result count for RepoBench runs; metrics still evaluate top 5")
	snapzipDiagnosticsLimit := fs.Int("snapzip-diagnostics-limit", 0, "Separate SnapZip diagnostic search result count; defaults to snapzip-search-limit")
	minRepobenchAcc1 := fs.String("min-repobench-snapzip-acc1", "", "Minimum SnapZip acc@1 for RepoBench-R")
	minRepobenchAcc3 := fs.String("min-repobench-snapzip-acc3", "", "Minimum SnapZip acc@3 for RepoBench-R")
	minRepobenchAcc5 := fs.String("min-repobench-snapzip-acc5", "", "Minimum SnapZip acc@5 for RepoBench-R")
	minRepobenchMRR5 := fs.String("min-repobench-snapzip-mrr5", "", "Minimum SnapZip MRR@5 for RepoBench-R")
	minRepobenchNDCG5 := fs.String("min-repobench-snapzip-ndcg5", "", "Minimum SnapZip nDCG@5 for RepoBench-R")
	maxRepobenchDuplicateTop5Records := fs.String("max-repobench-snapzip-duplicate-top5-records", "", "Maximum records with duplicate SnapZip top-5 results for RepoBench-R")
	maxRepobenchDuplicateTop5Slots := fs.String("max-repobench-snapzip-duplicate-top5-slots", "", "Maximum duplicate SnapZip top-5 result slots for RepoBench-R")
	minRepobenchAcc5OverBM25 := fs.String("min-repobench-snapzip-acc5-over-bm25", "", "Minimum SnapZip acc@5 delta over BM25 for RepoBench-R")
	minRepobenchMRR5OverBM25 := fs.String("min-repobench-snapzip-mrr5-over-bm25", "", "Minimum SnapZip MRR@5 delta over BM25 for RepoBench-R")
	minRepobenchNDCG5OverBM25 := fs.String("min-repobench-snapzip-ndcg5-over-bm25", "", "Minimum SnapZip nDCG@5 delta over BM25 for RepoBench-R")
	minRepobenchAcc5OverJaccard := fs.String("min-repobench-snapzip-acc5-over-jaccard", "", "Minimum SnapZip acc@5 delta over Jaccard for RepoBench-R")
	minRepobenchPGoldHit5 := fs.String("min-repobench-p-snapzip-gold-hit5", "", "Minimum SnapZip gold hit@5 for RepoBench v1.1")
	minRepobenchPNewTokenCoverage5 := fs.String("min-repobench-p-snapzip-new-token-coverage5", "", "Minimum SnapZip new-token coverage@5 for RepoBench v1.1")
	minRepobenchPIdentifierHit5 := fs.String("min-repobench-p-snapzip-identifier-hit5", "", "Minimum SnapZip gold-identifier hit@5 for RepoBench v1.1")
	minRepobenchPGoldHit5OverBM25 := fs.String("min-repobench-p-snapzip-gold-hit5-over-bm25", "", "Minimum SnapZip gold hit@5 delta over BM25 for RepoBench v1.1")
	minRepobenchPNewTokenCoverage5OverBM25 := fs.String("min-repobench-p-snapzip-new-token-coverage5-over-bm25", "", "Minimum SnapZip new-token coverage@5 delta over BM25 for RepoBench v1.1")
	liveCLICmd := fs.String("live-cli-cmd", "", "Local model CLI command for live completion eval; receives prompt on stdin unless it uses {prompt} or {prompt_file}")
	liveModel := fs.String("live-model", "", "Model label for reports; defaults to SNAPZIP_LIVE_MODEL or cli")
	liveSampleSize := fs.Int("live-sample-size", 20, "RepoBench live completion sample size")
	liveSeed := fs.Int("live-seed", 42, "RepoBench live completion sample seed")
	liveContextTopK := fs.Int("live-context-top-k", 5, "SnapZip context snippets to include in assisted prompt")
	liveTimeoutSeconds := fs.String("live-timeout-seconds", "120.0", "Timeout for each live model CLI call")
	liveCache := fs.String("live-cache", "", "Optional JSON cache path for live model calls")
	liveNoCache := fs.Bool("live-no-cache", false, "Disable live model response cache")
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
	if *keepWorkdir != "" {
		args = append(args, "--keep-workdir", *keepWorkdir)
	}
	appendOptionalStringFlag := func(name, value string) {
		if value != "" {
			args = append(args, name, value)
		}
	}
	appendIntFlag := func(name string, value int) {
		args = append(args, name, fmt.Sprint(value))
	}
	appendOptionalStringFlag("--repobench-data", *repobenchData)
	appendOptionalStringFlag("--repobench-config", *repobenchConfig)
	appendOptionalStringFlag("--repobench-split", *repobenchSplit)
	appendIntFlag("--repobench-sample-size", *repobenchSampleSize)
	appendIntFlag("--repobench-seed", *repobenchSeed)
	appendOptionalStringFlag("--repobench-matrix-configs", *repobenchMatrixConfigs)
	appendOptionalStringFlag("--repobench-matrix-splits", *repobenchMatrixSplits)
	appendOptionalStringFlag("--repobench-p-data", *repobenchPData)
	appendOptionalStringFlag("--repobench-p-language", *repobenchPLanguage)
	appendOptionalStringFlag("--repobench-p-split", *repobenchPSplit)
	appendIntFlag("--repobench-p-sample-size", *repobenchPSampleSize)
	appendIntFlag("--repobench-p-seed", *repobenchPSeed)
	appendIntFlag("--repobench-p-max-shards", *repobenchPMaxShards)
	appendOptionalStringFlag("--snapzip-rerank-cmd", *snapzipRerankCmd)
	if *snapzipDiagnostics {
		args = append(args, "--snapzip-diagnostics")
	}
	appendIntFlag("--snapzip-search-limit", *snapzipSearchLimit)
	appendIntFlag("--snapzip-diagnostics-limit", *snapzipDiagnosticsLimit)
	appendOptionalStringFlag("--min-repobench-snapzip-acc1", *minRepobenchAcc1)
	appendOptionalStringFlag("--min-repobench-snapzip-acc3", *minRepobenchAcc3)
	appendOptionalStringFlag("--min-repobench-snapzip-acc5", *minRepobenchAcc5)
	appendOptionalStringFlag("--min-repobench-snapzip-mrr5", *minRepobenchMRR5)
	appendOptionalStringFlag("--min-repobench-snapzip-ndcg5", *minRepobenchNDCG5)
	appendOptionalStringFlag("--max-repobench-snapzip-duplicate-top5-records", *maxRepobenchDuplicateTop5Records)
	appendOptionalStringFlag("--max-repobench-snapzip-duplicate-top5-slots", *maxRepobenchDuplicateTop5Slots)
	appendOptionalStringFlag("--min-repobench-snapzip-acc5-over-bm25", *minRepobenchAcc5OverBM25)
	appendOptionalStringFlag("--min-repobench-snapzip-mrr5-over-bm25", *minRepobenchMRR5OverBM25)
	appendOptionalStringFlag("--min-repobench-snapzip-ndcg5-over-bm25", *minRepobenchNDCG5OverBM25)
	appendOptionalStringFlag("--min-repobench-snapzip-acc5-over-jaccard", *minRepobenchAcc5OverJaccard)
	appendOptionalStringFlag("--min-repobench-p-snapzip-gold-hit5", *minRepobenchPGoldHit5)
	appendOptionalStringFlag("--min-repobench-p-snapzip-new-token-coverage5", *minRepobenchPNewTokenCoverage5)
	appendOptionalStringFlag("--min-repobench-p-snapzip-identifier-hit5", *minRepobenchPIdentifierHit5)
	appendOptionalStringFlag("--min-repobench-p-snapzip-gold-hit5-over-bm25", *minRepobenchPGoldHit5OverBM25)
	appendOptionalStringFlag("--min-repobench-p-snapzip-new-token-coverage5-over-bm25", *minRepobenchPNewTokenCoverage5OverBM25)
	appendOptionalStringFlag("--live-cli-cmd", *liveCLICmd)
	appendOptionalStringFlag("--live-model", *liveModel)
	appendIntFlag("--live-sample-size", *liveSampleSize)
	appendIntFlag("--live-seed", *liveSeed)
	appendIntFlag("--live-context-top-k", *liveContextTopK)
	appendOptionalStringFlag("--live-timeout-seconds", *liveTimeoutSeconds)
	appendOptionalStringFlag("--live-cache", *liveCache)
	if *liveNoCache {
		args = append(args, "--live-no-cache")
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
	pack, err := core.BuildRepairContextPack(db, comp, string(content), *query, *mode, *limit, *budget, 5)
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

func openDBOrExit(dbDir string) (*sql.DB, error) {
	db, err := core.InitDB(dbDir)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		return nil, err
	}
	_ = core.LazySyncIndex(db, dbDir)
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

func installMCPGlobal() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute executable path: %w", err)
	}

	configPath := getClaudeConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory for Claude config: %w", err)
	}

	var configMap map[string]any
	fileBytes, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			configMap = make(map[string]any)
		} else {
			return "", fmt.Errorf("failed to read Claude config: %w", err)
		}
	} else {
		if err := json.Unmarshal(fileBytes, &configMap); err != nil {
			return "", fmt.Errorf("Claude config contains invalid JSON, refusing to overwrite it: %w", err)
		}
	}

	mcpServers, ok := configMap["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
		configMap["mcpServers"] = mcpServers
	}

	mcpServers["snapzip"] = map[string]any{
		"command": exePath,
		"args":    []string{"mcp"},
	}

	newBytes, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode Claude config: %w", err)
	}

	if err := os.WriteFile(configPath, newBytes, 0644); err != nil {
		return "", fmt.Errorf("failed to write Claude config: %w", err)
	}

	return configPath, nil
}

func getClaudeConfigPath() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appdata, "Claude", "claude_desktop_config.json")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}
