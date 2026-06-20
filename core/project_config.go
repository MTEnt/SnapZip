package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const DefaultProjectConfigTOML = `# SnapZip project profile.
# Keep this file in source control when the whole team should share the same agent workflow.

[validation]
# Optional project-level command. SnapZip will suggest it, and will run it only with --run-config.
# command = "go test ./..."

[validation.commands]
# Optional language-specific commands.
# go = "go test ./..."
# py = "python -m pytest"
# js = "npm test"
# rb = "bundle exec rake test"
`

type ProjectConfig struct {
	Path       string                  `json:"path,omitempty"`
	Found      bool                    `json:"found"`
	Validation ProjectValidationConfig `json:"validation,omitempty"`
}

type ProjectValidationConfig struct {
	Command  string            `json:"command,omitempty"`
	Commands map[string]string `json:"commands,omitempty"`
}

func ProjectConfigPath(root string) string {
	return filepath.Join(root, ".snapzip", "config.toml")
}

func LoadProjectConfig(root string) (ProjectConfig, error) {
	path := ProjectConfigPath(root)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ProjectConfig{}, nil
	}
	if err != nil {
		return ProjectConfig{}, err
	}
	config, err := ParseProjectConfig(string(data))
	if err != nil {
		return ProjectConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	config.Path = path
	config.Found = true
	return config, nil
}

func ParseProjectConfig(content string) (ProjectConfig, error) {
	config := ProjectConfig{
		Validation: ProjectValidationConfig{Commands: map[string]string{}},
	}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(stripTOMLComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return ProjectConfig{}, fmt.Errorf("line %d: expected key = value", lineNumber)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		parsed, err := parseTOMLString(strings.TrimSpace(value))
		if err != nil {
			return ProjectConfig{}, fmt.Errorf("line %d: %w", lineNumber, err)
		}

		switch section {
		case "validation":
			if key == "command" {
				config.Validation.Command = parsed
			}
		case "validation.commands":
			if key != "" && parsed != "" {
				config.Validation.Commands[NormalizeLanguage(key)] = parsed
			}
		default:
			// Unknown sections are ignored so teams can add comments or future settings safely.
		}
	}
	if err := scanner.Err(); err != nil {
		return ProjectConfig{}, err
	}
	return config, nil
}

func WriteDefaultProjectConfig(root string, force bool) (string, bool, error) {
	path := ProjectConfigPath(root)
	if _, err := os.Stat(path); err == nil && !force {
		return path, false, nil
	} else if err != nil && !os.IsNotExist(err) {
		return path, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return path, false, err
	}
	if err := os.WriteFile(path, []byte(DefaultProjectConfigTOML), 0644); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func ConfiguredValidationCommands(config ProjectConfig, report AffectedReport) []SuggestedValidationCommand {
	var commands []SuggestedValidationCommand
	languages := validationLanguages(report)
	for _, language := range languages {
		if command := strings.TrimSpace(config.Validation.Commands[language]); command != "" {
			commands = append(commands, SuggestedValidationCommand{
				Command:    command,
				Reason:     "configured validation command for " + language,
				Confidence: 0.92,
			})
		}
	}
	if command := strings.TrimSpace(config.Validation.Command); command != "" {
		commands = append(commands, SuggestedValidationCommand{
			Command:    command,
			Reason:     "configured project validation command",
			Confidence: 0.90,
		})
	}
	return uniqueValidationCommands(commands)
}

func MergeValidationCommands(groups ...[]SuggestedValidationCommand) []SuggestedValidationCommand {
	var merged []SuggestedValidationCommand
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return uniqueValidationCommands(merged)
}

func validationLanguages(report AffectedReport) []string {
	seen := map[string]bool{}
	for _, path := range report.InputPaths {
		if language := NormalizeLanguage(LanguageFromPath(path)); language != "" {
			seen[language] = true
		}
	}
	for _, file := range report.Tests {
		if language := NormalizeLanguage(file.Language); language != "" {
			seen[language] = true
		}
	}
	for _, file := range report.Related {
		if language := NormalizeLanguage(file.Language); language != "" {
			seen[language] = true
		}
	}
	var languages []string
	for language := range seen {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

func parseTOMLString(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "\"") {
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", err
		}
		return value, nil
	}
	if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") && len(raw) >= 2 {
		return raw[1 : len(raw)-1], nil
	}
	return strings.TrimSpace(raw), nil
}

func stripTOMLComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for idx, r := range line {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inDouble:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case r == '#' && !inSingle && !inDouble:
			return line[:idx]
		}
	}
	return line
}
