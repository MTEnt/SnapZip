package core

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

var languageAliases = map[string]string{
	"assembly":        "asm",
	"bash":            "sh",
	"c#":              "cs",
	"c++":             "cpp",
	"clojure":         "clj",
	"cplusplus":       "cpp",
	"csharp":          "cs",
	"css3":            "css",
	"docker":          "dockerfile",
	"ecmascript":      "js",
	"elixir":          "ex",
	"erlang":          "erl",
	"f#":              "fs",
	"fsharp":          "fs",
	"golang":          "go",
	"graphqlschema":   "graphql",
	"haskell":         "hs",
	"html5":           "html",
	"javascript":      "js",
	"javascriptreact": "jsx",
	"julia":           "jl",
	"kotlin":          "kt",
	"matlab":          "m",
	"markdown":        "md",
	"node":            "js",
	"nodejs":          "js",
	"objectivec":      "m",
	"objectivecpp":    "mm",
	"objc":            "m",
	"objc++":          "mm",
	"perl":            "pl",
	"postgres":        "sql",
	"postgresql":      "sql",
	"protobuf":        "proto",
	"protocolbuffers": "proto",
	"python":          "py",
	"powershell":      "ps1",
	"rstats":          "r",
	"ruby":            "rb",
	"rust":            "rs",
	"shell":           "sh",
	"sqlite":          "sql",
	"terraform":       "tf",
	"typescript":      "ts",
	"typescriptreact": "tsx",
	"visualbasic":     "vb",
	"vbnet":           "vb",
	"yaml":            "yaml",
	"yml":             "yaml",
}

var defaultCodeLanguages = stringSet(
	"astro",
	"asm",
	"c",
	"cc",
	"cjs",
	"clj",
	"cljs",
	"cmake",
	"cpp",
	"cs",
	"css",
	"cxx",
	"dart",
	"dockerfile",
	"erl",
	"ex",
	"exs",
	"fish",
	"fs",
	"fsx",
	"go",
	"gql",
	"graphql",
	"groovy",
	"h",
	"hcl",
	"hh",
	"hpp",
	"hrl",
	"hs",
	"html",
	"hxx",
	"java",
	"jl",
	"js",
	"json",
	"json5",
	"jsonc",
	"jsx",
	"kt",
	"kts",
	"less",
	"lua",
	"m",
	"makefile",
	"md",
	"mdx",
	"mjs",
	"ml",
	"mli",
	"mm",
	"nix",
	"nim",
	"perl",
	"php",
	"pl",
	"pm",
	"prisma",
	"proto",
	"ps1",
	"py",
	"r",
	"rb",
	"rkt",
	"rs",
	"sass",
	"scala",
	"sc",
	"scss",
	"sh",
	"sol",
	"sql",
	"starlark",
	"svelte",
	"swift",
	"tf",
	"tfvars",
	"toml",
	"ts",
	"tsx",
	"vb",
	"vue",
	"xml",
	"yaml",
	"zig",
	"zsh",
	"bzl",
)

var languageGroups = map[string][]string{
	"backend": {"go", "java", "js", "kt", "php", "py", "rb", "rs", "scala", "ts"},
	"config":  {"dockerfile", "hcl", "json", "jsonc", "makefile", "tf", "toml", "yaml"},
	"frontend": {
		"astro", "css", "html", "js", "jsx", "less", "mdx", "sass", "scss", "svelte", "ts", "tsx", "vue",
	},
	"mobile": {"dart", "java", "kt", "swift"},
	"popular": {
		"c", "cpp", "cs", "css", "go", "html", "java", "js", "kt", "php", "py", "rb", "rs", "sql", "swift", "ts",
	},
	"systems": {"asm", "c", "cpp", "go", "rs", "zig"},
	"web":     {"astro", "css", "html", "js", "jsx", "less", "php", "sass", "scss", "svelte", "ts", "tsx", "vue"},
}

var specialFilenames = map[string]string{
	"build":           "starlark",
	"build.bazel":     "starlark",
	"cmakelists.txt":  "cmake",
	"containerfile":   "dockerfile",
	"dockerfile":      "dockerfile",
	"gemfile":         "rb",
	"gnumakefile":     "makefile",
	"jenkinsfile":     "groovy",
	"justfile":        "makefile",
	"makefile":        "makefile",
	"module.bazel":    "starlark",
	"package.json":    "json",
	"rakefile":        "rb",
	"tiltfile":        "starlark",
	"vagrantfile":     "rb",
	"workspace":       "starlark",
	"workspace.bazel": "starlark",
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
	if language, ok := specialFilenames[base]; ok {
		return language
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
		if group, ok := languageGroups[lang]; ok {
			for _, groupedLang := range group {
				langs[groupedLang] = true
			}
			continue
		}
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

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
