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

func TestAnalyzeFailureOutputParsesGoAndJSFrames(t *testing.T) {
	goTraceback := `panic: runtime error: slice bounds out of range
goroutine 1 [running]:
main.processUser(0x0, 0x1)
        myapp/main.go:42 +0x2d
main.main()
        myapp/main.go:87 +0xe5`

	jsTraceback := `Error: Something went wrong
    at processUser (myapp/main.js:42:15)
    at Object.<anonymous> (myapp/main.js:87:1)`

	// 1. Analyze Go traceback
	goAnalysis := AnalyzeFailureOutput(goTraceback, "")
	if !failureAnalysisHasRef(goAnalysis, "myapp/main.go", 42) {
		t.Fatalf("go traceback ref missing/incorrect: %+v", goAnalysis.FileRefs)
	}
	foundGoFunc := false
	for _, ref := range goAnalysis.FileRefs {
		if ref.Path == "myapp/main.go" && ref.Function == "processUser" {
			foundGoFunc = true
			break
		}
	}
	if !foundGoFunc {
		t.Fatalf("go function processUser not associated with frame: %+v", goAnalysis.FileRefs)
	}

	// 2. Analyze JS traceback
	jsAnalysis := AnalyzeFailureOutput(jsTraceback, "")
	if !failureAnalysisHasRef(jsAnalysis, "myapp/main.js", 42) {
		t.Fatalf("js traceback ref missing/incorrect: %+v", jsAnalysis.FileRefs)
	}
	foundJSFunc := false
	for _, ref := range jsAnalysis.FileRefs {
		if ref.Path == "myapp/main.js" && ref.Function == "processUser" {
			foundJSFunc = true
			break
		}
	}
	if !foundJSFunc {
		t.Fatalf("js function processUser not associated with frame: %+v", jsAnalysis.FileRefs)
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
