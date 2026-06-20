package core

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

var languageAliases = map[string]string{
	"bash":       "sh",
	"c++":        "cpp",
	"cplusplus":  "cpp",
	"csharp":     "cs",
	"c#":         "cs",
	"docker":     "dockerfile",
	"golang":     "go",
	"javascript": "js",
	"jsx":        "js",
	"node":       "js",
	"nodejs":     "js",
	"postgres":   "sql",
	"postgresql": "sql",
	"python":     "py",
	"ruby":       "rb",
	"rust":       "rs",
	"shell":      "sh",
	"sqlite":     "sql",
	"typescript": "ts",
	"tsx":        "ts",
}

var defaultCodeLanguages = map[string]bool{
	"bash":       true,
	"c":          true,
	"clj":        true,
	"cljs":       true,
	"cpp":        true,
	"cs":         true,
	"css":        true,
	"dart":       true,
	"dockerfile": true,
	"erl":        true,
	"ex":         true,
	"exs":        true,
	"fish":       true,
	"fs":         true,
	"fsx":        true,
	"go":         true,
	"graphql":    true,
	"h":          true,
	"hpp":        true,
	"hs":         true,
	"html":       true,
	"java":       true,
	"js":         true,
	"json":       true,
	"kt":         true,
	"kts":        true,
	"lua":        true,
	"m":          true,
	"makefile":   true,
	"md":         true,
	"ml":         true,
	"mli":        true,
	"mm":         true,
	"nim":        true,
	"php":        true,
	"proto":      true,
	"ps1":        true,
	"py":         true,
	"r":          true,
	"rb":         true,
	"rs":         true,
	"sass":       true,
	"scala":      true,
	"scss":       true,
	"sh":         true,
	"sql":        true,
	"svelte":     true,
	"swift":      true,
	"tf":         true,
	"toml":       true,
	"ts":         true,
	"vue":        true,
	"xml":        true,
	"yaml":       true,
	"yml":        true,
	"zig":        true,
	"zsh":        true,
}

type LanguageFilter struct {
	all       bool
	languages map[string]bool
}

func NormalizeLanguage(value string) string {
	lang := strings.ToLower(strings.TrimSpace(value))
	lang = strings.TrimPrefix(lang, ".")
	lang = strings.ReplaceAll(lang, "_", "")
	lang = strings.ReplaceAll(lang, "-", "")
	lang = strings.ReplaceAll(lang, " ", "")
	if lang == "" {
		return ""
	}
	if alias, ok := languageAliases[lang]; ok {
		return alias
	}
	return lang
}

func LanguageFromPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	}
	return NormalizeLanguage(filepath.Ext(path))
}

func NewLanguageFilter(input string) LanguageFilter {
	trimmed := strings.ToLower(strings.TrimSpace(input))
	if trimmed == "" || trimmed == "all" || trimmed == "any" || trimmed == "*" {
		return LanguageFilter{all: true}
	}

	langs := make(map[string]bool)
	for _, part := range strings.Split(trimmed, ",") {
		lang := NormalizeLanguage(part)
		if lang != "" {
			langs[lang] = true
		}
	}
	if len(langs) == 0 {
		return LanguageFilter{all: true}
	}
	return LanguageFilter{languages: langs}
}

func (f LanguageFilter) Matches(language string) bool {
	lang := NormalizeLanguage(language)
	if lang == "" {
		return false
	}
	if f.all {
		return defaultCodeLanguages[lang]
	}
	return f.languages[lang]
}

func (f LanguageFilter) Description() string {
	if f.all {
		return "all common code languages"
	}
	langs := make([]string, 0, len(f.languages))
	for lang := range f.languages {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	return strings.Join(langs, ",")
}

func languageTokens(input string) []string {
	return strings.FieldsFunc(strings.ToLower(input), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '#' || r == '+')
	})
}
