package core

import "testing"

func TestDetectNegativeFeedbackAvoidsCommonProgrammingTerms(t *testing.T) {
	cases := []string{
		"go error handling",
		"python failure_count variable",
		"failover routing",
		"no errors returned",
	}

	for _, input := range cases {
		if got := DetectNegativeFeedback(input); got != "" {
			t.Fatalf("DetectNegativeFeedback(%q) = %q, want empty", input, got)
		}
	}
}

func TestDetectNegativeFeedbackMatchesExplicitCritique(t *testing.T) {
	if got := DetectNegativeFeedback("this cache eviction logic is incorrect"); got != "incorrect" {
		t.Fatalf("DetectNegativeFeedback returned %q, want incorrect", got)
	}
	if got := DetectNegativeFeedback("the generated code is broken"); got != "broken" {
		t.Fatalf("DetectNegativeFeedback returned %q, want broken", got)
	}
}
