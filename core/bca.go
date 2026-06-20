package core

import (
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

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
		vocabulary: normalizeMutationVocabulary(vocab),
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
	checked, valid := CheckCompilation(code, filename)
	return !checked || valid
}

func CheckCompilation(code string, filename string) (bool, bool) {
	language := LanguageFromPath(filename)
	checker, ok := syntaxCheckerForLanguage(language)
	if !ok {
		return false, true
	}
	if _, err := exec.LookPath(checker.executable); err != nil {
		return false, true
	}

	temp, err := os.CreateTemp("", "snapzip-*"+syntaxTempSuffix(language, filename))
	if err != nil {
		return true, false
	}
	tempFile := temp.Name()
	defer os.Remove(tempFile)

	if _, err := temp.WriteString(code); err != nil {
		_ = temp.Close()
		return true, false
	}
	if err := temp.Close(); err != nil {
		return true, false
	}

	cmd := exec.Command(checker.executable, checker.args(tempFile)...)
	return true, cmd.Run() == nil
}

func CanCheckCompilation(filename string) bool {
	language := LanguageFromPath(filename)
	checker, ok := syntaxCheckerForLanguage(language)
	if !ok {
		return false
	}
	_, err := exec.LookPath(checker.executable)
	return err == nil
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

// Mutate proposes a conservative code transition X -> X' by replacing one
// identifier with an identifier observed in local context.
func (o *BCAOptimizer) Mutate(draft []byte, r *rand.Rand) []byte {
	if len(draft) == 0 {
		return draft
	}
	if len(o.vocabulary) == 0 {
		return cloneBytes(draft)
	}

	spans := identifierSpans(draft)
	if len(spans) == 0 {
		return cloneBytes(draft)
	}

	span := spans[r.Intn(len(spans))]
	token := []byte(o.vocabulary[r.Intn(len(o.vocabulary))])
	mutated := make([]byte, 0, len(draft)-span.length()+len(token))
	mutated = append(mutated, draft[:span.start]...)
	mutated = append(mutated, token...)
	mutated = append(mutated, draft[span.end:]...)
	return mutated
}

// Optimize runs the Metropolis-Hastings MCMC sampling loop
func (o *BCAOptimizer) Optimize(seedCode string, filename string) string {
	if !CanCheckCompilation(filename) {
		return seedCode
	}
	if _, valid := CheckCompilation(seedCode, filename); !valid {
		return seedCode
	}

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
			if _, valid := CheckCompilation(string(proposal), filename); !valid {
				continue
			}

			current = proposal
			currentScore = propScore

			if currentScore < bestScore {
				best = make([]byte, len(current))
				copy(best, current)
				bestScore = currentScore
			}
		}
	}

	return string(best)
}

type byteSpan struct {
	start int
	end   int
}

func (s byteSpan) length() int {
	return s.end - s.start
}

func identifierSpans(input []byte) []byteSpan {
	var spans []byteSpan
	start := -1
	for idx, r := range string(input) {
		if start == -1 {
			if isIdentifierStart(r) {
				start = idx
			}
			continue
		}
		if !isIdentifierPart(r) {
			token := string(input[start:idx])
			if !protectedIdentifier(token) {
				spans = append(spans, byteSpan{start: start, end: idx})
			}
			start = -1
		}
	}
	if start != -1 {
		token := string(input[start:])
		if !protectedIdentifier(token) {
			spans = append(spans, byteSpan{start: start, end: len(input)})
		}
	}
	return spans
}

func normalizeMutationVocabulary(values []string) []string {
	seen := make(map[string]bool)
	vocab := make([]string, 0, len(values))
	for _, value := range values {
		token := strings.TrimSpace(value)
		if !isIdentifierToken(token) || protectedIdentifier(token) || seen[token] {
			continue
		}
		seen[token] = true
		vocab = append(vocab, token)
		if len(vocab) >= 4096 {
			break
		}
	}
	return vocab
}

func isIdentifierToken(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for idx, r := range value {
		if idx == 0 {
			if !isIdentifierStart(r) {
				return false
			}
			continue
		}
		if !isIdentifierPart(r) {
			return false
		}
	}
	return true
}

func isIdentifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentifierPart(r rune) bool {
	return isIdentifierStart(r) || unicode.IsDigit(r)
}

func protectedIdentifier(token string) bool {
	return protectedIdentifiers[token]
}

var protectedIdentifiers = stringSet(
	"False", "None", "True",
	"abstract", "any", "as", "assert", "async", "await",
	"bool", "boolean", "break", "byte",
	"case", "catch", "char", "class", "const", "continue",
	"debugger", "defer", "def", "default", "delete", "do", "double",
	"elif", "else", "enum", "except", "export", "extends",
	"fallthrough", "false", "final", "finally", "float", "for", "from", "func", "function",
	"go", "goto",
	"if", "implements", "import", "in", "instanceof", "int", "interface", "is",
	"lambda", "let", "long",
	"map", "module",
	"namespace", "new", "nil", "null",
	"operator", "or",
	"package", "pass", "private", "protected", "public",
	"raise", "range", "return",
	"select", "short", "static", "struct", "super", "switch",
	"this", "throw", "throws", "trait", "true", "try", "type", "typeof",
	"union", "unsafe", "use", "using",
	"var", "void",
	"while", "with",
	"yield",
)

func cloneBytes(input []byte) []byte {
	output := make([]byte, len(input))
	copy(output, input)
	return output
}
