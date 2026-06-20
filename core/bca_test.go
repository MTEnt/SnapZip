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
