package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MTEnt/SnapZip/core"
	"github.com/klauspost/compress/zstd"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	switch subcommand {
	case "init-db":
		handleInitDB()
	case "search":
		handleSearch()
	case "optimize":
		handleOptimize()
	case "stats":
		handleStats()
	case "log-feedback":
		handleLogFeedback()
	case "get-feedback":
		handleGetFeedback()
	default:
		fmt.Printf("Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: snapzip <subcommand> [options]")
	fmt.Println("Subcommands:")
	fmt.Println("  init-db        Initialize the local memory database and index project directories")
	fmt.Println("  search         Search template database using Hybrid FTS5+QND ranking")
	fmt.Println("  optimize       Refine code sketches using Bayesian Zstd MCMC")
	fmt.Println("  stats          Show indexed row counts and language breakdown")
	fmt.Println("  log-feedback   Log negative user feedback to database")
	fmt.Println("  get-feedback   Retrieve recent negative feedback entries to guide LLM")
}

func handleInitDB() {
	fs := flag.NewFlagSet("init-db", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory to store memory.db")
	langs := fs.String("langs", "", "Comma-separated language names/extensions to index, or all/any")
	crawl := fs.String("crawl", "", "Codebase directory to crawl and index")
	reset := fs.Bool("reset", false, "Remove any existing memory.db before initializing")
	maxFileBytes := fs.Int64("max-file-bytes", core.DefaultMaxIndexFileBytes, "Maximum individual source file size to index")
	_ = fs.Parse(os.Args[2:])
	langsProvided := flagWasProvided(fs, "langs")
	crawlProvided := flagWasProvided(fs, "crawl")

	langInput := strings.TrimSpace(*langs)
	codebasePath := strings.TrimSpace(*crawl)

	if !langsProvided && !crawlProvided {
		// Interactive Onboarding Questionnaire
		reader := bufio.NewReader(os.Stdin)
		fmt.Println("==================================================")
		fmt.Println("        Welcome to SnapZip Setup                 ")
		fmt.Println("==================================================")

		fmt.Printf("\n1. Where should we store the memory.db file? [Default: %s]: ", *dbDir)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			*dbDir = input
		}

		fmt.Print("2. Which languages/extensions do you want to support? (e.g., go, py, js, python, rust) [Default: all]: ")
		langInput, _ = reader.ReadString('\n')
		langInput = strings.TrimSpace(langInput)

		fmt.Print("3. Path to your codebase directory to crawl and index [Default: none]: ")
		codebasePath, _ = reader.ReadString('\n')
		codebasePath = strings.TrimSpace(codebasePath)
	}
	if langInput == "" {
		langInput = "all"
	}
	langFilter := core.NewLanguageFilter(langInput)

	if *reset {
		if err := core.ResetDB(*dbDir); err != nil {
			fmt.Printf("Error resetting DB: %v\n", err)
			os.Exit(1)
		}
	}

	// Initialize the Database
	db, err := core.InitDB(*dbDir)
	if err != nil {
		fmt.Printf("Error initializing DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Printf("\nInitialized memory.db in: %s/memory.db\n", *dbDir)
	fmt.Printf("Target languages filter: %s\n", langFilter.Description())

	// Crawl and index codebase files immediately if requested
	if codebasePath != "" {
		fmt.Printf("\nIndexing files under: %s...\n", codebasePath)
		options := core.DefaultIndexOptions()
		options.MaxFileBytes = *maxFileBytes
		entryCount, err := core.IndexDirectoryWithOptions(db, codebasePath, langFilter, options)

		if err != nil {
			fmt.Printf("Error indexing codebase files: %v\n", err)
		} else {
			fmt.Printf("Indexed %d entries into memory.db\n", entryCount)
		}
	}
}

func handleSearch() {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	query := fs.String("query", "", "Search query string")
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	limit := fs.Int("limit", 3, "Number of snippets to return")
	_ = fs.Parse(os.Args[2:])

	if *query == "" {
		fmt.Println("Error: --query is required")
		fs.Usage()
		os.Exit(1)
	}

	db, err := core.InitDB(*dbDir)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Retrieve and print past negative feedback to alert the user/agent.
	feedbacks, err := core.RetrieveNegativeFeedback(db, 5)
	if err == nil && len(feedbacks) > 0 {
		fmt.Fprintln(os.Stderr, "\n[SnapZip Memory Warning] Avoid repeating these past mistakes/failures:")
		for _, f := range feedbacks {
			if f.BotResponse != "" {
				fmt.Fprintf(os.Stderr, "   - Problem: %q | Failed Output: %q\n", f.UserInput, f.BotResponse)
			} else {
				fmt.Fprintf(os.Stderr, "   - Problem: %q\n", f.UserInput)
			}
		}
		fmt.Fprintln(os.Stderr)
	}

	comp, err := core.NewZstdCompressor(zstd.SpeedDefault)
	if err != nil {
		fmt.Printf("Error initializing compressor: %v\n", err)
		os.Exit(1)
	}

	results, err := core.RetrieveSimilarSnippets(db, comp, *query, *limit)
	if err != nil {
		fmt.Printf("Search failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d matching snippets:\n", len(results))
	for _, res := range results {
		fmt.Printf("\n--- Topic: %s (Language: %s | QND Score: %.4f) ---\n%s\n", res.Topic, res.Language, res.Score, res.Content)
	}
}

func handleOptimize() {
	fs := flag.NewFlagSet("optimize", flag.ExitOnError)
	sketchFile := fs.String("sketch", "", "Path to the seed code sketch file")
	contextDir := fs.String("context", "", "Directory containing codebase context files")
	outputFile := fs.String("output", "", "Path to write the optimized code")
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	langs := fs.String("langs", "all", "Comma-separated language names/extensions to load from context")
	iterations := fs.Int("iter", 10000, "Number of MCMC iterations")
	temp := fs.Float64("temp", 0.15, "MCMC temperature parameter")
	priorWeight := fs.Float64("prior-weight", 1.0, "Weight of the prior grammar check")
	_ = fs.Parse(os.Args[2:])

	if *sketchFile == "" || *contextDir == "" || *outputFile == "" {
		fmt.Println("Error: --sketch, --context, and --output are required")
		fs.Usage()
		os.Exit(1)
	}

	// 1. Read seed sketch code
	sketchBytes, err := os.ReadFile(*sketchFile)
	if err != nil {
		fmt.Printf("Error reading sketch file: %v\n", err)
		os.Exit(1)
	}

	// Automatically check and print negative feedback warnings to guide optimizer
	db, err := core.InitDB(*dbDir)
	if err == nil {
		defer db.Close()
		feedbacks, err := core.RetrieveNegativeFeedback(db, 5)
		if err == nil && len(feedbacks) > 0 {
			fmt.Fprintln(os.Stderr, "\n[SnapZip Optimizer Warning] Checked negative feedback memory. Avoid repeating these past failures:")
			for _, f := range feedbacks {
				if f.BotResponse != "" {
					fmt.Fprintf(os.Stderr, "   - Problem: %q | Failed Output: %q\n", f.UserInput, f.BotResponse)
				} else {
					fmt.Fprintf(os.Stderr, "   - Problem: %q\n", f.UserInput)
				}
			}
			fmt.Fprintln(os.Stderr)
		}
	}

	// 2. Build dictionary and mutation vocabulary from context directory.
	context, err := core.LoadContextDirectory(*contextDir, core.NewLanguageFilter(*langs), core.DefaultContextLimitBytes)
	if err != nil {
		fmt.Printf("Error scanning context: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Context size loaded: %d bytes from %d files (Vocabulary: %d unique tokens)\n", len(context.Data), context.FileCount, len(context.Vocabulary))
	fmt.Printf("Optimizing seed code from %s using Zstd raw dictionary priming (MCMC Mode)...\n", *sketchFile)

	// 3. Run optimizer
	cfg := core.BCAConfig{
		MaxIterations: *iterations,
		Temperature:   *temp,
		PriorWeight:   *priorWeight,
	}

	opt, err := core.NewBCAOptimizer(cfg, context.Data, context.Vocabulary)
	if err != nil {
		fmt.Printf("Error building BCA optimizer: %v\n", err)
		os.Exit(1)
	}

	optimized := opt.Optimize(string(sketchBytes), filepath.Base(*sketchFile))

	// 4. Save optimized code
	err = os.WriteFile(*outputFile, []byte(optimized), 0644)
	if err != nil {
		fmt.Printf("Error writing output file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Success: Optimized code saved to %s\n", *outputFile)
}

func handleStats() {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	_ = fs.Parse(os.Args[2:])

	db, err := core.InitDB(*dbDir)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	stats, err := core.GetDatabaseStats(db)
	if err != nil {
		fmt.Printf("Error reading stats: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("knowledge rows: %d\n", stats.KnowledgeRows)
	fmt.Printf("feedback rows: %d\n", stats.FeedbackRows)
	if len(stats.Languages) == 0 {
		fmt.Println("languages: none")
		return
	}
	fmt.Println("languages:")
	for _, lang := range stats.Languages {
		fmt.Printf("  %s: %d\n", lang.Language, lang.Count)
	}
}

func handleLogFeedback() {
	fs := flag.NewFlagSet("log-feedback", flag.ExitOnError)
	input := fs.String("input", "", "User's feedback/critique text")
	botResponse := fs.String("bot-response", "", "The bot response that prompted negative feedback")
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	_ = fs.Parse(os.Args[2:])

	if *input == "" {
		fmt.Println("Error: --input is required")
		fs.Usage()
		os.Exit(1)
	}

	db, err := core.InitDB(*dbDir)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	logged, err := core.AddFeedback(db, *input, *botResponse)
	if err != nil {
		fmt.Printf("Error logging feedback: %v\n", err)
		os.Exit(1)
	}

	if logged {
		fmt.Println("Success: Negative feedback logged to memory.db database")
	} else {
		fmt.Println("Feedback analyzed: Neutral/positive statement. No negative sentiment indexed.")
	}
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func handleGetFeedback() {
	fs := flag.NewFlagSet("get-feedback", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	limit := fs.Int("limit", 10, "Number of negative feedback entries to return")
	_ = fs.Parse(os.Args[2:])

	db, err := core.InitDB(*dbDir)
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	list, err := core.RetrieveNegativeFeedback(db, *limit)
	if err != nil {
		fmt.Printf("Error retrieving feedback: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d negative feedback entries in memory.db:\n", len(list))
	for _, entry := range list {
		fmt.Printf("\n[%s] Sentiment: '%s'\n  User Feedback: \"%s\"\n  Bot Output: \"%s\"\n", entry.CreatedAt, entry.Sentiment, entry.UserInput, entry.BotResponse)
	}
}
