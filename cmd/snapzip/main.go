package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"snapzip/core"
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
	fmt.Println("  log-feedback   Log negative user feedback & frustrations to database")
	fmt.Println("  get-feedback   Retrieve recent negative feedback entries to guide LLM")
}

func handleInitDB() {
	fs := flag.NewFlagSet("init-db", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory to store memory.db")
	_ = fs.Parse(os.Args[2:])

	// Interactive Onboarding Questionnaire
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("==================================================")
	fmt.Println("        ⚡ Welcome to SnapZip Setup! ⚡           ")
	fmt.Println("==================================================")

	fmt.Printf("\n1. Where should we store the memory.db file? [Default: %s]: ", *dbDir)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input != "" {
		*dbDir = input
	}

	fmt.Print("2. Which languages/extensions do you want to support? (e.g., go, py, js) [Default: all]: ")
	langInput, _ := reader.ReadString('\n')
	langInput = strings.ToLower(strings.TrimSpace(langInput))
	if langInput == "" {
		langInput = "all"
	}

	fmt.Print("3. Path to your codebase directory to crawl and index [Default: none]: ")
	codebasePath, _ := reader.ReadString('\n')
	codebasePath = strings.TrimSpace(codebasePath)

	// Initialize the Database
	db, err := core.InitDB(*dbDir)
	if err != nil {
		fmt.Printf("Error initializing DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Printf("\n✓ Successfully initialized memory.db in: %s/memory.db\n", *dbDir)
	fmt.Printf("✓ Target languages filter: %s\n", langInput)

	// Crawl and index codebase files immediately if requested
	if codebasePath != "" {
		fmt.Printf("\nIndexing files under: %s...\n", codebasePath)
		fileCount := 0

		err = filepath.Walk(codebasePath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.TrimPrefix(filepath.Ext(path), ".")
			
			// Filter by user languages
			match := false
			if langInput == "all" {
				match = (ext == "go" || ext == "py" || ext == "js" || ext == "ts" || ext == "sql" || ext == "sh")
			} else {
				for _, filter := range strings.Split(langInput, ",") {
					if strings.TrimSpace(filter) == ext {
						match = true
						break
					}
				}
			}

			if match {
				content, err := os.ReadFile(path)
				if err == nil {
					topic := fmt.Sprintf("Source file: %s", filepath.Base(path))
					err = core.AddKnowledge(db, ext, topic, string(content))
					if err == nil {
						fileCount++
					}
				}
			}
			return nil
		})

		if err != nil {
			fmt.Printf("Error indexing codebase files: %v\n", err)
		} else {
			fmt.Printf("✓ Successfully indexed %d files into memory.db!\n", fileCount)
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

	// Automatically analyze and log query if it contains negative sentiment/complaints
	_, _ = core.AddFeedback(db, *query, "")

	// Automatically retrieve and print past negative feedback to alert the user/agent
	feedbacks, err := core.RetrieveNegativeFeedback(db, 5)
	if err == nil && len(feedbacks) > 0 {
		fmt.Fprintln(os.Stderr, "\n⚠️  [SnapZip Memory Warning] Avoid repeating these past mistakes/failures:")
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
	db, err := core.InitDB(".")
	if err == nil {
		defer db.Close()
		feedbacks, err := core.RetrieveNegativeFeedback(db, 5)
		if err == nil && len(feedbacks) > 0 {
			fmt.Fprintln(os.Stderr, "\n⚠️  [SnapZip Optimizer Warning] Checked negative feedback memory. Avoid repeating these past failures:")
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

	// 2. Build mock dictionary from context directory
	var contextBuf strings.Builder
	vocabMap := make(map[string]bool)

	err = filepath.Walk(*contextDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".go" || ext == ".py" || ext == ".js" || ext == ".ts" {
			content, err := os.ReadFile(path)
			if err == nil {
				contextBuf.Write(content)
				contextBuf.WriteString("\n")
				
				// Build vocabulary map for mutations
				words := strings.Fields(string(content))
				for _, w := range words {
					if len(w) > 3 && len(w) < 20 {
						vocabMap[w] = true
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Error scanning context: %v\n", err)
		os.Exit(1)
	}

	contextData := []byte(contextBuf.String())
	if len(contextData) > 2*1024*1024 {
		contextData = contextData[:2*1024*1024]
	}

	var vocab []string
	for w := range vocabMap {
		vocab = append(vocab, w)
	}

	fmt.Printf("Context size loaded: %d bytes (Vocabulary: %d unique tokens)\n", len(contextData), len(vocab))
	fmt.Printf("Optimizing seed code from %s using Zstd raw dictionary priming (MCMC Mode)...\n", *sketchFile)

	// 3. Run optimizer
	cfg := core.BCAConfig{
		MaxIterations: *iterations,
		Temperature:   *temp,
		PriorWeight:   *priorWeight,
	}

	opt, err := core.NewBCAOptimizer(cfg, contextData, vocab)
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

	fmt.Printf("✓ Success: Optimized code saved to %s\n", *outputFile)
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
		fmt.Println("✓ Success: Negative feedback logged to memory.db database")
	} else {
		fmt.Println("Feedback analyzed: Neutral/positive statement. No negative sentiment indexed.")
	}
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
