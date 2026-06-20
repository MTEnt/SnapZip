package examples

import (
	"bytes"
	"testing"

	"github.com/MTEnt/SnapZip/core"
)

func BenchmarkBCACompress(b *testing.B) {
	// 1. Generate mock 2 MB codebase context
	var contextBuf bytes.Buffer
	baseText := `
package main
import "fmt"
func calculateFibonacci(n int) int {
	if n <= 1 { return n }
	return calculateFibonacci(n-1) + calculateFibonacci(n-2)
}
`
	for contextBuf.Len() < 2*1024*1024 {
		contextBuf.WriteString(baseText)
	}
	contextBytes := contextBuf.Bytes()

	// 2. Generate target draft code (2 KB)
	draftText := `
func main() {
	fmt.Println("Hello, SnapZip world!")
}
`
	var draftBuf bytes.Buffer
	for draftBuf.Len() < 2*1024 {
		draftBuf.WriteString(draftText)
	}
	draftBytes := draftBuf.Bytes()

	cfg := core.BCAConfig{
		MaxIterations: 1,
		Temperature:   0.15,
		PriorWeight:   1.0,
	}

	opt, err := core.NewBCAOptimizer(cfg, contextBytes, []string{"fmt", "main", "func", "println"})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = opt.CompressDraft(draftBytes)
		}
	})
}
