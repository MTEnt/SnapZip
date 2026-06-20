package core

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
)

type SuggestedValidationCommand struct {
	Command    string  `json:"command"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

type ValidationPlan struct {
	InputPaths        []string                     `json:"input_paths"`
	Affected          AffectedReport               `json:"affected"`
	SuggestedCommands []SuggestedValidationCommand `json:"suggested_commands,omitempty"`
}

func BuildValidationPlan(db *sql.DB, paths []string, limit int) (ValidationPlan, error) {
	report, err := FindAffectedTests(db, paths, limit)
	if err != nil {
		return ValidationPlan{}, err
	}
	return ValidationPlan{
		InputPaths:        report.InputPaths,
		Affected:          report,
		SuggestedCommands: SuggestValidationCommands(report),
	}, nil
}

func SuggestValidationCommands(report AffectedReport) []SuggestedValidationCommand {
	var commands []SuggestedValidationCommand
	commands = append(commands, suggestGoValidation(report)...)
	commands = append(commands, suggestPythonValidation(report)...)
	commands = append(commands, suggestJavaScriptValidation(report)...)
	commands = append(commands, suggestRubyValidation(report)...)
	return uniqueValidationCommands(commands)
}

func suggestGoValidation(report AffectedReport) []SuggestedValidationCommand {
	paths := validationPathsForLanguage(report, "go")
	if len(paths) == 0 {
		return nil
	}

	dirs := uniqueSortedDirs(paths, ".go")
	if len(dirs) == 0 {
		return nil
	}

	return []SuggestedValidationCommand{{
		Command:    "go test " + strings.Join(formatGoPackageArgs(dirs), " "),
		Reason:     "Go source or tests are likely affected",
		Confidence: 0.85,
	}}
}

func suggestPythonValidation(report AffectedReport) []SuggestedValidationCommand {
	tests := validationTestPaths(report, "py")
	if len(tests) == 0 && hasInputLanguage(report, "py") {
		return []SuggestedValidationCommand{{
			Command:    "python -m pytest",
			Reason:     "Python files changed but no indexed test file was identified",
			Confidence: 0.45,
		}}
	}
	if len(tests) == 0 {
		return nil
	}
	return []SuggestedValidationCommand{{
		Command:    "python -m pytest " + joinCommandArgs(tests),
		Reason:     "Python tests are likely affected",
		Confidence: 0.78,
	}}
}

func suggestJavaScriptValidation(report AffectedReport) []SuggestedValidationCommand {
	tests := validationTestPaths(report, "js", "jsx", "ts", "tsx")
	if len(tests) == 0 && hasInputLanguage(report, "js", "jsx", "ts", "tsx") {
		return []SuggestedValidationCommand{{
			Command:    "npm test",
			Reason:     "JavaScript or TypeScript files changed but no indexed test file was identified",
			Confidence: 0.40,
		}}
	}
	if len(tests) == 0 {
		return nil
	}
	return []SuggestedValidationCommand{{
		Command:    "npm test -- " + joinCommandArgs(tests),
		Reason:     "JavaScript or TypeScript tests are likely affected",
		Confidence: 0.65,
	}}
}

func suggestRubyValidation(report AffectedReport) []SuggestedValidationCommand {
	tests := validationTestPaths(report, "rb")
	if len(tests) == 0 && hasInputLanguage(report, "rb") {
		return []SuggestedValidationCommand{{
			Command:    "bundle exec rake test",
			Reason:     "Ruby files changed but no indexed test file was identified",
			Confidence: 0.40,
		}}
	}
	if len(tests) == 0 {
		return nil
	}

	var specs []string
	for _, path := range tests {
		if strings.Contains(path, "/spec/") || strings.HasSuffix(path, "_spec.rb") {
			specs = append(specs, path)
		}
	}
	if len(specs) > 0 {
		return []SuggestedValidationCommand{{
			Command:    "bundle exec rspec " + joinCommandArgs(specs),
			Reason:     "Ruby specs are likely affected",
			Confidence: 0.72,
		}}
	}
	return []SuggestedValidationCommand{{
		Command:    "bundle exec rake test",
		Reason:     "Ruby tests are likely affected",
		Confidence: 0.62,
	}}
}

func validationPathsForLanguage(report AffectedReport, languages ...string) []string {
	allowed := languageSet(languages...)
	var paths []string
	for _, test := range report.Tests {
		if allowed[NormalizeLanguage(test.Language)] {
			paths = append(paths, test.Path)
		}
	}
	for _, related := range report.Related {
		if allowed[NormalizeLanguage(related.Language)] {
			paths = append(paths, related.Path)
		}
	}
	for _, path := range report.InputPaths {
		if allowed[NormalizeLanguage(LanguageFromPath(path))] {
			paths = append(paths, path)
		}
	}
	return uniqueStrings(paths)
}

func validationTestPaths(report AffectedReport, languages ...string) []string {
	allowed := languageSet(languages...)
	var tests []string
	for _, test := range report.Tests {
		if allowed[NormalizeLanguage(test.Language)] {
			tests = append(tests, test.Path)
		}
	}
	return uniqueStrings(tests)
}

func hasInputLanguage(report AffectedReport, languages ...string) bool {
	allowed := languageSet(languages...)
	for _, path := range report.InputPaths {
		if allowed[NormalizeLanguage(LanguageFromPath(path))] {
			return true
		}
	}
	return false
}

func languageSet(languages ...string) map[string]bool {
	result := map[string]bool{}
	for _, language := range languages {
		result[NormalizeLanguage(language)] = true
	}
	return result
}

func uniqueSortedDirs(paths []string, ext string) []string {
	seen := map[string]bool{}
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if filepath.Ext(path) != ext {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		if dir == "." {
			dir = ""
		}
		seen[dir] = true
	}
	var dirs []string
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func formatGoPackageArgs(dirs []string) []string {
	args := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			args = append(args, ".")
			continue
		}
		args = append(args, shellQuoteArg("./"+dir))
	}
	return args
}

func joinCommandArgs(paths []string) string {
	paths = uniqueStrings(paths)
	sort.Strings(paths)
	args := make([]string, 0, len(paths))
	for _, path := range paths {
		args = append(args, shellQuoteArg(path))
	}
	return strings.Join(args, " ")
}

func shellQuoteArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:-", r))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func uniqueValidationCommands(commands []SuggestedValidationCommand) []SuggestedValidationCommand {
	seen := map[string]bool{}
	var result []SuggestedValidationCommand
	for _, command := range commands {
		command.Command = strings.TrimSpace(command.Command)
		if command.Command == "" || seen[command.Command] {
			continue
		}
		seen[command.Command] = true
		result = append(result, command)
	}
	return result
}
