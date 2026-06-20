package core

import (
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
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
	dictID     uint32
	dictBytes  []byte
	encoder    *zstd.Encoder
	vocabulary []string
}

func NewBCAOptimizer(cfg BCAConfig, dictBytes []byte, vocab []string) (*BCAOptimizer, error) {
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
		dictID:     dictID,
		dictBytes:  dictBytes,
		encoder:    enc,
		vocabulary: vocab,
	}, nil
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
	tempFile := filepath.Join(os.TempDir(), "snapzip_temp_"+filename)
	err := os.WriteFile(tempFile, []byte(code), 0644)
	if err != nil {
		return false
	}
	defer os.Remove(tempFile)

	ext := filepath.Ext(filename)
	var cmd *exec.Cmd
	switch ext {
	case ".go":
		cmd = exec.Command("go", "vet", tempFile)
	case ".py":
		cmd = exec.Command("python3", "-m", "py_compile", tempFile)
	case ".js":
		cmd = exec.Command("node", "--check", tempFile)
	default:
		return true // skip for unsupported extensions
	}

	err = cmd.Run()
	return err == nil
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
			if r.Float64() < prob {
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
