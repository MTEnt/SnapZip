package core

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const maxFailureAnalysisTerms = 80

type FailureFileRef struct {
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Function string `json:"function,omitempty"`
}

type FailureAnalysis struct {
	Query       string           `json:"query"`
	FileRefs    []FailureFileRef `json:"file_refs,omitempty"`
	Symbols     []string         `json:"symbols,omitempty"`
	Identifiers []string         `json:"identifiers,omitempty"`
}

var (
	pythonFramePattern      = regexp.MustCompile(`File\s+"([^"]+)",\s+line\s+([0-9]+),\s+in\s+([A-Za-z_][A-Za-z0-9_]*)`)
	genericLocationPattern  = regexp.MustCompile(`([A-Za-z0-9_./\\-]+\.(?:go|py|js|jsx|ts|tsx|rb|java|rs|php|swift|kt|c|cc|cpp|h|hpp|cs|css|html|sql|sh|bash|zsh|md|yaml|yml|json|toml|xml))(?::([0-9]+))?`)
	identifierPattern       = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
	callPattern             = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	quotedIdentifierPattern = regexp.MustCompile(`['"]([A-Za-z_][A-Za-z0-9_]{2,})['"]`)
)

func AnalyzeFailureOutput(output, extra string) FailureAnalysis {
	var analysis FailureAnalysis
	var queryTerms []string
	if strings.TrimSpace(extra) != "" {
		queryTerms = append(queryTerms, strings.TrimSpace(extra))
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		analysis.addPythonFrames(line, &queryTerms)
		analysis.addGenericLocations(line, &queryTerms)
		analysis.addQuotedIdentifiers(line, &queryTerms)
		analysis.addCallSymbols(line, &queryTerms)
		analysis.addWords(line, &queryTerms)
	}

	analysis.FileRefs = uniqueFailureFileRefs(analysis.FileRefs)
	analysis.Symbols = uniqueFailureTerms(analysis.Symbols)
	analysis.Identifiers = uniqueFailureTerms(analysis.Identifiers)
	analysis.Query = strings.Join(limitFailureTerms(uniqueFailureTerms(queryTerms), maxFailureAnalysisTerms), " ")
	return analysis
}

func (analysis *FailureAnalysis) addPythonFrames(line string, queryTerms *[]string) {
	for _, match := range pythonFramePattern.FindAllStringSubmatch(line, -1) {
		lineNumber, _ := strconv.Atoi(match[2])
		path := cleanFailurePath(match[1])
		functionName := strings.TrimSpace(match[3])
		analysis.FileRefs = append(analysis.FileRefs, FailureFileRef{
			Path:     path,
			Line:     lineNumber,
			Function: functionName,
		})
		*queryTerms = append(*queryTerms, path, functionName)
		if !commonFailureSymbol(functionName) {
			analysis.Symbols = append(analysis.Symbols, functionName)
		}
	}
}

func (analysis *FailureAnalysis) addGenericLocations(line string, queryTerms *[]string) {
	for _, match := range genericLocationPattern.FindAllStringSubmatch(line, -1) {
		path := cleanFailurePath(match[1])
		if path == "" {
			continue
		}
		lineNumber := 0
		if len(match) > 2 && match[2] != "" {
			lineNumber, _ = strconv.Atoi(match[2])
		}
		if lineNumber > 0 {
			analysis.FileRefs = append(analysis.FileRefs, FailureFileRef{Path: path, Line: lineNumber})
		}
		*queryTerms = append(*queryTerms, path)
	}
}

func (analysis *FailureAnalysis) addQuotedIdentifiers(line string, queryTerms *[]string) {
	for _, match := range quotedIdentifierPattern.FindAllStringSubmatch(line, -1) {
		identifier := strings.TrimSpace(match[1])
		if identifier == "" || commonFailureWord(identifier) {
			continue
		}
		analysis.Identifiers = append(analysis.Identifiers, identifier)
		*queryTerms = append(*queryTerms, identifier)
	}
}

func (analysis *FailureAnalysis) addCallSymbols(line string, queryTerms *[]string) {
	for _, match := range callPattern.FindAllStringSubmatch(line, -1) {
		symbol := strings.TrimSpace(match[1])
		if symbol == "" || commonFailureSymbol(symbol) {
			continue
		}
		analysis.Symbols = append(analysis.Symbols, symbol)
		*queryTerms = append(*queryTerms, symbol)
	}
}

func (analysis *FailureAnalysis) addWords(line string, queryTerms *[]string) {
	for _, word := range identifierPattern.FindAllString(line, -1) {
		lower := strings.ToLower(word)
		if commonFailureWord(lower) {
			continue
		}
		if looksLikeErrorType(word) || strings.Contains(word, "_") {
			analysis.Identifiers = append(analysis.Identifiers, word)
		}
		*queryTerms = append(*queryTerms, word)
	}
}

func cleanFailurePath(path string) string {
	path = strings.TrimSpace(strings.Trim(path, "\"'`"))
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

func looksLikeErrorType(word string) bool {
	return strings.HasSuffix(word, "Error") ||
		strings.HasSuffix(word, "Exception") ||
		strings.HasSuffix(word, "Failure") ||
		strings.HasSuffix(word, "Panic")
}

func commonFailureWord(word string) bool {
	switch strings.ToLower(word) {
	case "actual", "assert", "asserted", "build", "case", "error", "errors", "exit", "expected", "fail", "failed", "failure", "file", "line", "none", "null", "panic", "status", "test", "tests", "trace", "traceback", "true", "false":
		return true
	default:
		return false
	}
}

func commonFailureSymbol(symbol string) bool {
	switch strings.ToLower(symbol) {
	case "assert", "assertequal", "assertfalse", "assertin", "assertis", "assertisnone", "assertisnotnone", "assertraises", "asserttrue", "bool", "dict", "format", "int", "len", "list", "open", "print", "set", "str", "super", "tuple":
		return true
	default:
		return commonFailureWord(symbol)
	}
}

func uniqueFailureTerms(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func limitFailureTerms(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func uniqueFailureFileRefs(values []FailureFileRef) []FailureFileRef {
	seen := map[string]bool{}
	result := make([]FailureFileRef, 0, len(values))
	for _, value := range values {
		value.Path = strings.TrimSpace(value.Path)
		if value.Path == "" {
			continue
		}
		key := strings.ToLower(value.Path) + ":" + strconv.Itoa(value.Line) + ":" + strings.ToLower(value.Function)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}
