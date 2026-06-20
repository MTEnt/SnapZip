package core

import (
	"strings"
	"testing"
)

func TestAnalyzeFailureOutputScansStructuredRefsPastQueryCap(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < maxFailureAnalysisTerms+20; i++ {
		builder.WriteString("noise_token ")
	}
	builder.WriteString("\npysnooper/pysnooper.py:26: NameError: name 'output_path' is not defined\n")

	analysis := AnalyzeFailureOutput(builder.String(), "")
	if len(strings.Fields(analysis.Query)) > maxFailureAnalysisTerms {
		t.Fatalf("query has %d terms, want at most %d", len(strings.Fields(analysis.Query)), maxFailureAnalysisTerms)
	}
	if !failureAnalysisHasRef(analysis, "pysnooper/pysnooper.py", 26) {
		t.Fatalf("analysis missed source ref after query cap: %+v", analysis.FileRefs)
	}
	if !failureAnalysisHasIdentifier(analysis, "output_path") {
		t.Fatalf("analysis missed undefined identifier: %+v", analysis.Identifiers)
	}
}

func failureAnalysisHasRef(analysis FailureAnalysis, path string, line int) bool {
	for _, ref := range analysis.FileRefs {
		if ref.Path == path && ref.Line == line {
			return true
		}
	}
	return false
}

func failureAnalysisHasIdentifier(analysis FailureAnalysis, identifier string) bool {
	for _, value := range analysis.Identifiers {
		if value == identifier {
			return true
		}
	}
	return false
}
