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

func TestBuildRepairContextPackPrefersSourceSymbolExcerpt(t *testing.T) {
	db, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	source := strings.Join([]string{
		"def unrelated_live_filter(info):",
		"    return info.get('is_live')",
		"",
		strings.Repeat("def filler():\n    return 'is_live'\n\n", 20),
		"def match_str(filter_str, dct):",
		"    if filter_str == 'is_live':",
		"        return dct.get('is_live') is not None",
		"    return False",
	}, "\n")
	testSource := strings.Join([]string{
		"def test_match_str():",
		"    assert not match_str('is_live', {'is_live': False})",
	}, "\n")
	unrelated := strings.Repeat("is_live extractor archived stream metadata\n", 80)

	if _, err := AddKnowledgeContent(db, "py", "Source file: youtube_dl/utils.py", "youtube_dl/utils.py", []byte(source), 512, 0); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSymbolsForFile(db, "py", "youtube_dl/utils.py", []byte(source)); err != nil {
		t.Fatal(err)
	}
	if _, err := AddKnowledgeContent(db, "py", "Source file: test/test_utils.py", "test/test_utils.py", []byte(testSource), 512, 0); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSymbolsForFile(db, "py", "test/test_utils.py", []byte(testSource)); err != nil {
		t.Fatal(err)
	}
	if _, err := AddKnowledgeContent(db, "py", "Source file: youtube_dl/extractor/bambuser.py", "youtube_dl/extractor/bambuser.py", []byte(unrelated), 512, 0); err != nil {
		t.Fatal(err)
	}

	failure := strings.Join([]string{
		"Traceback (most recent call last):",
		`  File "/tmp/work/test/test_utils.py", line 2, in test_match_str`,
		"    assert not match_str('is_live', {'is_live': False})",
		"AssertionError: True is not false",
	}, "\n")
	pack, err := BuildRepairContextPack(db, mustTestCompressor(t), failure, "", "debug", 4, 4096, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Snippets) == 0 {
		t.Fatal("got no repair snippets")
	}
	if pack.Snippets[0].Path != "youtube_dl/utils.py" {
		t.Fatalf("top repair snippet = %q, want source function:\n%s", pack.Snippets[0].Path, RenderContextPack(pack))
	}
	if !strings.Contains(pack.Snippets[0].Content, "def match_str") {
		t.Fatalf("top repair snippet did not include focused function:\n%s", pack.Snippets[0].Content)
	}
	if strings.Contains(pack.Snippets[0].Path, "bambuser") {
		t.Fatalf("unrelated extractor ranked first:\n%s", RenderContextPack(pack))
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
