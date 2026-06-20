package core

import (
	"fmt"
	"strings"
)

type ContextQuality struct {
	Score     float64               `json:"score"`
	Grade     string                `json:"grade"`
	Summary   string                `json:"summary"`
	Metrics   ContextQualityMetrics `json:"metrics"`
	Strengths []string              `json:"strengths,omitempty"`
	Warnings  []string              `json:"warnings,omitempty"`
}

type ContextQualityMetrics struct {
	SnippetCount           int     `json:"snippet_count"`
	SourceSnippetCount     int     `json:"source_snippet_count"`
	TestSnippetCount       int     `json:"test_snippet_count"`
	DependencySnippetCount int     `json:"dependency_snippet_count"`
	DefinitionCount        int     `json:"definition_count"`
	ReferenceCount         int     `json:"reference_count"`
	ReceiptCount           int     `json:"receipt_count"`
	ReceiptCoverage        float64 `json:"receipt_coverage"`
	EvidenceCount          int     `json:"evidence_count"`
	EvidenceDensity        float64 `json:"evidence_density"`
	UniquePathCount        int     `json:"unique_path_count"`
	UniquePathRatio        float64 `json:"unique_path_ratio"`
	DuplicatePathCount     int     `json:"duplicate_path_count"`
	FeedbackCount          int     `json:"feedback_count"`
	BudgetUtilization      float64 `json:"budget_utilization"`
	Truncated              bool    `json:"truncated"`
}

func ScoreContextPack(pack ContextPack) ContextQuality {
	metrics := contextQualityMetrics(pack)
	score := 0.0
	if metrics.SnippetCount > 0 {
		score = 0.18
	}
	score += 0.18 * metrics.ReceiptCoverage
	score += 0.12 * cappedRatio(float64(metrics.DefinitionCount), 2)
	score += 0.10 * cappedRatio(float64(metrics.ReferenceCount), 2)
	score += 0.12 * cappedRatio(float64(metrics.TestSnippetCount), 1)
	score += 0.10 * metrics.UniquePathRatio
	score += 0.14 * cappedRatio(metrics.EvidenceDensity, 2)
	if metrics.FeedbackCount > 0 {
		score += 0.04
	}
	if !metrics.Truncated && metrics.SnippetCount > 0 {
		score += 0.08
	}
	if metrics.DependencySnippetCount > 0 && metrics.DependencySnippetCount >= metrics.SourceSnippetCount {
		score -= 0.10
	}
	score = clampFloat(score, 0, 1)

	quality := ContextQuality{
		Score:   roundFloat(score),
		Grade:   contextQualityGrade(score),
		Metrics: metrics,
	}
	quality.Strengths, quality.Warnings = contextQualityNotes(pack, metrics)
	quality.Summary = contextQualitySummary(quality)
	return quality
}

func contextQualityMetrics(pack ContextPack) ContextQualityMetrics {
	metrics := ContextQualityMetrics{
		SnippetCount:  len(pack.Snippets),
		ReceiptCount:  len(pack.Receipts),
		FeedbackCount: len(pack.Feedback),
		Truncated:     pack.Truncated,
	}

	paths := map[string]bool{}
	for _, snippet := range pack.Snippets {
		pathKey := snippet.Path
		if pathKey == "" {
			pathKey = snippet.Topic
		}
		if pathKey != "" {
			paths[pathKey] = true
		}
		if isTestPath(snippet.Path) {
			metrics.TestSnippetCount++
		} else if isDependencyPath(snippet.Path) {
			metrics.DependencySnippetCount++
		} else {
			metrics.SourceSnippetCount++
		}
		metrics.DefinitionCount += len(ExtractSymbols(snippet.Language, snippet.Path, snippet.Content))
		metrics.ReferenceCount += len(ExtractSymbolReferences(snippet.Language, snippet.Path, snippet.Content))
	}
	metrics.UniquePathCount = len(paths)
	if metrics.SnippetCount > 0 {
		metrics.ReceiptCoverage = roundFloat(cappedRatio(float64(metrics.ReceiptCount), float64(metrics.SnippetCount)))
		metrics.UniquePathRatio = roundFloat(cappedRatio(float64(metrics.UniquePathCount), float64(metrics.SnippetCount)))
		metrics.DuplicatePathCount = metrics.SnippetCount - metrics.UniquePathCount
	}

	for _, receipt := range pack.Receipts {
		metrics.EvidenceCount += len(receipt.Evidence)
	}
	if metrics.SnippetCount > 0 {
		metrics.EvidenceDensity = roundFloat(float64(metrics.EvidenceCount) / float64(metrics.SnippetCount))
	}
	if pack.BudgetBytes > 0 && pack.UsedBytes > 0 {
		metrics.BudgetUtilization = roundFloat(cappedRatio(float64(pack.UsedBytes), float64(pack.BudgetBytes)))
	}
	return metrics
}

func contextQualityNotes(pack ContextPack, metrics ContextQualityMetrics) ([]string, []string) {
	var strengths []string
	var warnings []string

	if metrics.ReceiptCoverage >= 1 && metrics.SnippetCount > 0 {
		strengths = append(strengths, "every included snippet has a context receipt")
	} else if metrics.SnippetCount > 0 {
		warnings = append(warnings, "some snippets do not have context receipts")
	}
	if metrics.DefinitionCount > 0 {
		strengths = append(strengths, "included snippets contain indexed definitions")
	} else if metrics.SnippetCount > 0 {
		warnings = append(warnings, "no indexed definitions were detected in the included snippets")
	}
	if metrics.ReferenceCount > 0 {
		strengths = append(strengths, "included snippets contain call/reference sites")
	} else if pack.Mode == "refactor" || pack.Mode == "review" {
		warnings = append(warnings, pack.Mode+" mode did not include detected call/reference sites")
	}
	if metrics.TestSnippetCount > 0 {
		strengths = append(strengths, "test context is present")
	} else if pack.Mode == "debug" || pack.Mode == "test" || pack.Mode == "review" {
		warnings = append(warnings, pack.Mode+" context has no test snippet")
	}
	if metrics.EvidenceDensity >= 1 {
		strengths = append(strengths, "receipts include direct evidence")
	} else if pack.Mode == "debug" || pack.Mode == "review" {
		warnings = append(warnings, pack.Mode+" context has sparse receipt evidence")
	}
	if metrics.DuplicatePathCount > 0 {
		warnings = append(warnings, "multiple snippets came from the same path")
	}
	if metrics.DependencySnippetCount > 0 {
		warnings = append(warnings, "dependency snippets are present")
	}
	if metrics.Truncated {
		warnings = append(warnings, "pack was truncated to fit the requested budget")
	}
	return uniqueStrings(strengths), uniqueStrings(warnings)
}

func contextQualitySummary(quality ContextQuality) string {
	if quality.Metrics.SnippetCount == 0 {
		return "No snippets were included, so context quality is weak."
	}
	return fmt.Sprintf(
		"%s context: %d snippet(s), %.0f%% receipt coverage, %d definition(s), %d reference site(s), %d test snippet(s).",
		quality.Grade,
		quality.Metrics.SnippetCount,
		quality.Metrics.ReceiptCoverage*100,
		quality.Metrics.DefinitionCount,
		quality.Metrics.ReferenceCount,
		quality.Metrics.TestSnippetCount,
	)
}

func shouldRenderContextQuality(quality ContextQuality) bool {
	return quality.Metrics.SnippetCount > 0 || len(quality.Warnings) > 0 || len(quality.Strengths) > 0
}

func renderContextQuality(builder *strings.Builder, quality ContextQuality) {
	builder.WriteString("\n## Context Quality\n\n")
	fmt.Fprintf(builder, "Score: %.2f (%s)\n", quality.Score, quality.Grade)
	if quality.Summary != "" {
		fmt.Fprintf(builder, "Summary: %s\n", quality.Summary)
	}
	fmt.Fprintf(
		builder,
		"Metrics: receipts %.0f%%, evidence %.2f/snippet, unique paths %.0f%%, budget %.0f%%\n",
		quality.Metrics.ReceiptCoverage*100,
		quality.Metrics.EvidenceDensity,
		quality.Metrics.UniquePathRatio*100,
		quality.Metrics.BudgetUtilization*100,
	)
	fmt.Fprintf(
		builder,
		"Coverage: definitions %d, references %d, tests %d, dependencies %d\n",
		quality.Metrics.DefinitionCount,
		quality.Metrics.ReferenceCount,
		quality.Metrics.TestSnippetCount,
		quality.Metrics.DependencySnippetCount,
	)
	if len(quality.Strengths) > 0 {
		builder.WriteString("Strengths:\n")
		for _, strength := range quality.Strengths {
			fmt.Fprintf(builder, "- %s\n", strength)
		}
	}
	if len(quality.Warnings) > 0 {
		builder.WriteString("Warnings:\n")
		for _, warning := range quality.Warnings {
			fmt.Fprintf(builder, "- %s\n", warning)
		}
	}
}

func contextQualityGrade(score float64) string {
	switch {
	case score >= 0.75:
		return "strong"
	case score >= 0.55:
		return "usable"
	case score >= 0.35:
		return "thin"
	default:
		return "weak"
	}
}

func cappedRatio(numerator, denominator float64) float64 {
	if denominator <= 0 || numerator <= 0 {
		return 0
	}
	return clampFloat(numerator/denominator, 0, 1)
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func roundFloat(value float64) float64 {
	if value < 0 {
		return -roundFloat(-value)
	}
	return float64(int(value*100+0.5)) / 100
}
