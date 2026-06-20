package core

import (
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

type BCAConfig struct {
	MaxIterations int     `json:"max_iterations"`
	Temperature   float64 `json:"temperature"`
	PriorWeight   float64 `json:"prior_weight"`
}

// BCAOptimizer implements the Bayesian Compression Agent optimization loop
type BCAOptimizer struct {
	config     BCAConfig
	encoder    *zstd.Encoder
	vocabulary []string
}

type syntaxChecker struct {
	executable string
	args       func(path string) []string
}

var syntaxCheckers = map[string]syntaxChecker{
	"c":     {"cc", func(path string) []string { return []string{"-fsyntax-only", path} }},
	"cc":    {"c++", func(path string) []string { return []string{"-fsyntax-only", path} }},
	"cpp":   {"c++", func(path string) []string { return []string{"-fsyntax-only", path} }},
	"cxx":   {"c++", func(path string) []string { return []string{"-fsyntax-only", path} }},
	"go":    {"go", func(path string) []string { return []string{"vet", path} }},
	"js":    {"node", func(path string) []string { return []string{"--check", path} }},
	"mjs":   {"node", func(path string) []string { return []string{"--check", path} }},
	"cjs":   {"node", func(path string) []string { return []string{"--check", path} }},
	"lua":   {"luac", func(path string) []string { return []string{"-p", path} }},
	"perl":  {"perl", func(path string) []string { return []string{"-c", path} }},
	"pl":    {"perl", func(path string) []string { return []string{"-c", path} }},
	"pm":    {"perl", func(path string) []string { return []string{"-c", path} }},
	"php":   {"php", func(path string) []string { return []string{"-l", path} }},
	"py":    {"python3", func(path string) []string { return []string{"-m", "py_compile", path} }},
	"rb":    {"ruby", func(path string) []string { return []string{"-c", path} }},
	"sh":    {"sh", func(path string) []string { return []string{"-n", path} }},
	"swift": {"swiftc", func(path string) []string { return []string{"-parse", path} }},
	"ts":    {"tsc", func(path string) []string { return []string{"--noEmit", "--pretty", "false", path} }},
	"tsx": {"tsc", func(path string) []string {
		return []string{"--noEmit", "--pretty", "false", "--jsx", "preserve", path}
	}},
	"zsh": {"zsh", func(path string) []string { return []string{"-n", path} }},
}

func NewBCAOptimizer(cfg BCAConfig, dictBytes []byte, vocab []string) (*BCAOptimizer, error) {
	cfg = normalizeBCAConfig(cfg)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	dictID := r.Uint32()

	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderDictRaw(dictID, dictBytes),
	)
	if err != nil {
		return nil, err
	}
	return &BCAOptimizer{
		config:     cfg,
		encoder:    enc,
		vocabulary: vocab,
	}, nil
}

func normalizeBCAConfig(cfg BCAConfig) BCAConfig {
	if cfg.MaxIterations < 0 {
		cfg.MaxIterations = 0
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = 0.15
	}
	if cfg.PriorWeight < 0 {
		cfg.PriorWeight = 1.0
	}
	return cfg
}

// CompressDraft calculates compressed size C(X | Y) using the pre-primed dictionary
func (o *BCAOptimizer) CompressDraft(draft []byte) int {
	return len(o.encoder.EncodeAll(draft, nil))
}

// PriorScore evaluates the syntax/grammar penalty score of a draft.
// Lower score is better (representing fewer syntactic violations).
func (o *BCAOptimizer) PriorScore(draft []byte) float64 {
	brackets := 0
	penalties := 0.0
	for _, b := range draft {
		if b == '{' || b == '(' || b == '[' {
			brackets++
		} else if b == '}' || b == ')' || b == ']' {
			brackets--
			if brackets < 0 {
				penalties += 10.0 // heavily penalize unmatched closing brackets
			}
		}
	}
	if brackets != 0 {
		penalties += float64(math.Abs(float64(brackets))) * 5.0
	}
	return penalties
}

// VerifyCompilation runs the compiler/linter check on a temporary file
func VerifyCompilation(code string, filename string) bool {
	language := LanguageFromPath(filename)
	checker, ok := syntaxCheckerForLanguage(language)
	if !ok {
		return true
	}
	if _, err := exec.LookPath(checker.executable); err != nil {
		return true
	}

	temp, err := os.CreateTemp("", "snapzip-*"+syntaxTempSuffix(language, filename))
	if err != nil {
		return false
	}
	tempFile := temp.Name()
	defer os.Remove(tempFile)

	if _, err := temp.WriteString(code); err != nil {
		_ = temp.Close()
		return false
	}
	if err := temp.Close(); err != nil {
		return false
	}

	cmd := exec.Command(checker.executable, checker.args(tempFile)...)
	return cmd.Run() == nil
}

func syntaxCheckerForLanguage(language string) (syntaxChecker, bool) {
	checker, ok := syntaxCheckers[NormalizeLanguage(language)]
	return checker, ok
}

func syntaxTempSuffix(language, filename string) string {
	if ext := filepath.Ext(filename); ext != "" {
		return ext
	}
	lang := NormalizeLanguage(language)
	switch {
	case lang == "dockerfile" || lang == "makefile" || lang == "starlark":
		return ".txt"
	case lang == "":
		return ".txt"
	case strings.ContainsAny(lang, `/\`):
		return ".txt"
	default:
		return "." + lang
	}
}

// Mutate proposes a code transition X -> X' (swapping tokens or tweaking characters)
func (o *BCAOptimizer) Mutate(draft []byte, r *rand.Rand) []byte {
	if len(draft) == 0 {
		return draft
	}
	mutated := make([]byte, len(draft))
	copy(mutated, draft)

	choice := r.Intn(3)
	if choice == 0 && len(o.vocabulary) > 0 {
		// Swap a token from vocabulary
		token := o.vocabulary[r.Intn(len(o.vocabulary))]
		offset := r.Intn(len(mutated))
		end := offset + len(token)
		if end <= len(mutated) {
			copy(mutated[offset:end], []byte(token))
		}
	} else {
		// Single character tweak
		offset := r.Intn(len(mutated))
		mutated[offset] = byte(32 + r.Intn(95)) // printable ascii
	}
	return mutated
}

// Optimize runs the Metropolis-Hastings MCMC sampling loop
func (o *BCAOptimizer) Optimize(seedCode string, filename string) string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	current := []byte(seedCode)

	currentCompSize := o.CompressDraft(current)
	currentPrior := o.PriorScore(current)
	currentScore := float64(currentCompSize) + o.config.PriorWeight*currentPrior

	best := make([]byte, len(current))
	copy(best, current)
	bestScore := currentScore

	for i := 0; i < o.config.MaxIterations; i++ {
		proposal := o.Mutate(current, r)

		propCompSize := o.CompressDraft(proposal)
		propPrior := o.PriorScore(proposal)
		propScore := float64(propCompSize) + o.config.PriorWeight*propPrior

		delta := propScore - currentScore

		// Metropolis-Hastings acceptance check
		accept := false
		if delta <= 0 {
			accept = true
		} else {
			prob := math.Exp(-delta / o.config.Temperature)
			if !math.IsNaN(prob) && r.Float64() < prob {
				accept = true
			}
		}

		if accept {
			current = proposal
			currentScore = propScore

			// Corrected verification check: verify compilation on every improvement before writing to best
			if currentScore < bestScore {
				if VerifyCompilation(string(current), filename) {
					best = make([]byte, len(current))
					copy(best, current)
					bestScore = currentScore
				} else {
					// Apply score penalty to force MCMC to backtrack
					currentScore += 100.0
				}
			}
		}
	}

	return string(best)
}
