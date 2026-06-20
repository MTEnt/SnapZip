package core

import "testing"

func TestNormalizeBCAConfig(t *testing.T) {
	cfg := normalizeBCAConfig(BCAConfig{
		MaxIterations: -1,
		Temperature:   0,
		PriorWeight:   -2,
	})

	if cfg.MaxIterations != 0 {
		t.Fatalf("MaxIterations = %d, want 0", cfg.MaxIterations)
	}
	if cfg.Temperature != 0.15 {
		t.Fatalf("Temperature = %f, want 0.15", cfg.Temperature)
	}
	if cfg.PriorWeight != 1.0 {
		t.Fatalf("PriorWeight = %f, want 1.0", cfg.PriorWeight)
	}
}

func TestVerifyCompilationPython(t *testing.T) {
	if !VerifyCompilation("def ok():\n    return 1\n", "example.py") {
		t.Fatal("valid Python failed compilation check")
	}
	if VerifyCompilation("def broken(:\n", "example.py") {
		t.Fatal("invalid Python passed compilation check")
	}
}

func TestCheckCompilationReportsAvailability(t *testing.T) {
	checked, valid := CheckCompilation("def ok():\n    return 1\n", "example.py")
	if !checked || !valid {
		t.Fatalf("CheckCompilation valid Python = checked %v, valid %v; want true, true", checked, valid)
	}

	checked, valid = CheckCompilation("<main>no checker</main>\n", "example.html")
	if checked || !valid {
		t.Fatalf("CheckCompilation HTML = checked %v, valid %v; want false, true", checked, valid)
	}
}

func TestOptimizeReturnsSeedWithoutSyntaxChecker(t *testing.T) {
	opt, err := NewBCAOptimizer(BCAConfig{MaxIterations: 100}, []byte("context"), []string{"replacement"})
	if err != nil {
		t.Fatal(err)
	}

	seed := "<main>Keep this exact HTML</main>\n"
	if got := opt.Optimize(seed, "index.html"); got != seed {
		t.Fatalf("Optimize without syntax checker changed seed:\n%s", got)
	}
}

func TestOptimizeKeepsPythonSyntaxValid(t *testing.T) {
	opt, err := NewBCAOptimizer(
		BCAConfig{MaxIterations: 200, Temperature: 0.15, PriorWeight: 1},
		[]byte("def total(items):\n    return sum(items)\n"),
		[]string{"total", "items", "sum", "value"},
	)
	if err != nil {
		t.Fatal(err)
	}

	seed := "def total(items):\n    return sum(items)\n"
	got := opt.Optimize(seed, "example.py")
	if !VerifyCompilation(got, "example.py") {
		t.Fatalf("optimized Python failed syntax check:\n%s", got)
	}
}

func TestSyntaxCheckerForPopularLanguages(t *testing.T) {
	cases := map[string]string{
		"c":     "cc",
		"cpp":   "c++",
		"go":    "go",
		"js":    "node",
		"php":   "php",
		"py":    "python3",
		"rb":    "ruby",
		"sh":    "sh",
		"swift": "swiftc",
		"ts":    "tsc",
	}

	for language, wantExecutable := range cases {
		checker, ok := syntaxCheckerForLanguage(language)
		if !ok {
			t.Fatalf("expected syntax checker for %q", language)
		}
		if checker.executable != wantExecutable {
			t.Fatalf("checker for %q uses %q, want %q", language, checker.executable, wantExecutable)
		}
	}

	if _, ok := syntaxCheckerForLanguage("html"); ok {
		t.Fatal("did not expect built-in syntax checker for html")
	}
}

func TestSyntaxTempSuffixForSpecialFilenames(t *testing.T) {
	if got := syntaxTempSuffix("rb", "Gemfile"); got != ".rb" {
		t.Fatalf("Gemfile suffix = %q, want .rb", got)
	}
	if got := syntaxTempSuffix("dockerfile", "Dockerfile"); got != ".txt" {
		t.Fatalf("Dockerfile suffix = %q, want .txt", got)
	}
}
