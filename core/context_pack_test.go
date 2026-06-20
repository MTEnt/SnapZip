package core

import (
	"strings"
	"testing"
)

func TestBuildContextPackRespectsBudget(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	content := strings.Repeat("class CacheStore\n  def fetch\n    :ok\n  end\nend\n", 80)
	if err := AddKnowledge(db, "rb", "Source file: lib/cache.rb", content); err != nil {
		t.Fatal(err)
	}

	pack, err := BuildContextPack(db, mustTestCompressor(t), "ruby CacheStore fetch", 1, 900, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Snippets) != 1 {
		t.Fatalf("got %d snippets, want 1", len(pack.Snippets))
	}
	if !pack.Truncated {
		t.Fatal("expected context pack to report truncation")
	}
	if pack.UsedBytes > pack.BudgetBytes {
		t.Fatalf("pack used %d bytes, budget %d", pack.UsedBytes, pack.BudgetBytes)
	}
	if !strings.Contains(RenderContextPack(pack), "CacheStore") {
		t.Fatal("rendered pack did not include expected content")
	}
}

func TestBuildContextPackBoundsLargeFeedback(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := AddKnowledge(db, "go", "Source file: core/cache.go", "func CacheStore() {}\n"); err != nil {
		t.Fatal(err)
	}
	logged, err := AddFeedback(db, "wrong "+strings.Repeat("feedback ", 300), strings.Repeat("failed output ", 300))
	if err != nil {
		t.Fatal(err)
	}
	if !logged {
		t.Fatal("expected feedback to be logged")
	}

	pack, err := BuildContextPack(db, mustTestCompressor(t), "go CacheStore", 1, 1200, 5)
	if err != nil {
		t.Fatal(err)
	}
	if pack.UsedBytes > pack.BudgetBytes {
		t.Fatalf("pack used %d bytes, budget %d", pack.UsedBytes, pack.BudgetBytes)
	}
}

func TestExpandQueryForPackMode(t *testing.T) {
	if got := ExpandQueryForPackMode("cache", "debug"); !strings.Contains(got, "failure") {
		t.Fatalf("debug mode did not add failure terms: %q", got)
	}
	if got := ExpandQueryForPackMode("cache", "refactor"); !strings.Contains(got, "caller") {
		t.Fatalf("refactor mode did not add caller terms: %q", got)
	}
	if got := ExpandQueryForPackMode("cache", "test"); !strings.Contains(got, "assertion") {
		t.Fatalf("test mode did not add assertion terms: %q", got)
	}
	if got := ExpandQueryForPackMode("cache", "docs"); !strings.Contains(got, "documentation") {
		t.Fatalf("docs mode did not add documentation terms: %q", got)
	}
}
