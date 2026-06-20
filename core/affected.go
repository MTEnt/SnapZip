package core

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
)

type AffectedFile struct {
	Path       string   `json:"path"`
	Language   string   `json:"language,omitempty"`
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons"`
}

type AffectedReport struct {
	InputPaths []string       `json:"input_paths"`
	Tests      []AffectedFile `json:"tests"`
	Related    []AffectedFile `json:"related,omitempty"`
}

func FindAffectedTests(db *sql.DB, paths []string, limit int) (AffectedReport, error) {
	limit = normalizeResultLimit(limit, 10)
	inputs := normalizeAffectedPaths(paths)
	report := AffectedReport{InputPaths: inputs}
	if len(inputs) == 0 {
		return report, nil
	}

	indexed, err := indexedPathLanguages(db)
	if err != nil {
		return report, err
	}

	scored := map[string]AffectedFile{}
	related := map[string]AffectedFile{}
	for _, path := range inputs {
		if isTestPath(path) {
			addAffected(scored, path, indexed[path], 0.95, "input path is already a test file")
		}
		for _, candidate := range directTestCandidates(path) {
			if language, ok := indexed[candidate]; ok {
				addAffected(scored, candidate, language, 0.85, "direct test naming convention matches changed source")
			}
		}

		files, err := RelatedFiles(db, path, limit*4)
		if err != nil {
			return report, err
		}
		for _, file := range files {
			confidence := 0.60
			reason := "shares indexed symbols with changed source"
			if isTestPath(file.Path) {
				confidence = 0.78
				addAffected(scored, file.Path, file.Language, confidence, reason)
				continue
			}
			addAffected(related, file.Path, file.Language, confidence, reason)
		}
	}

	report.Tests = affectedMapToSortedList(scored, limit)
	report.Related = affectedMapToSortedList(related, limit)
	return report, nil
}

func normalizeAffectedPaths(paths []string) []string {
	var values []string
	for _, path := range paths {
		for _, part := range strings.Split(path, ",") {
			part = strings.TrimSpace(strings.ReplaceAll(part, "\\", "/"))
			part = strings.TrimPrefix(part, "./")
			if part != "" {
				values = append(values, part)
			}
		}
	}
	return uniqueStrings(values)
}

func indexedPathLanguages(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`
		SELECT path, language
		FROM knowledge
		WHERE path != ''
		GROUP BY path, language`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]string{}
	for rows.Next() {
		var path, language string
		if err := rows.Scan(&path, &language); err != nil {
			return nil, err
		}
		result[path] = language
	}
	return result, rows.Err()
}

func directTestCandidates(path string) []string {
	path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
	dir := filepath.ToSlash(filepath.Dir(path))
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if dir == "." {
		dir = ""
	}
	join := func(parts ...string) string {
		return strings.TrimPrefix(filepath.ToSlash(filepath.Join(parts...)), "./")
	}

	var candidates []string
	switch ext {
	case ".go":
		candidates = append(candidates, join(dir, stem+"_test.go"))
	case ".py":
		candidates = append(candidates,
			join(dir, "test_"+stem+".py"),
			join("tests", "test_"+stem+".py"),
			join("test", "test_"+stem+".py"),
			join(dir, stem+"_test.py"),
		)
	case ".js", ".jsx", ".ts", ".tsx":
		for _, testExt := range []string{".test" + ext, ".spec" + ext} {
			candidates = append(candidates, join(dir, stem+testExt))
		}
		candidates = append(candidates,
			join("__tests__", base),
			join(dir, "__tests__", base),
		)
	case ".rb":
		candidates = append(candidates,
			join("test", stem+"_test.rb"),
			join("spec", stem+"_spec.rb"),
			join(dir, stem+"_test.rb"),
			join(dir, stem+"_spec.rb"),
		)
	}
	return uniqueStrings(candidates)
}

func addAffected(target map[string]AffectedFile, path, language string, confidence float64, reason string) {
	if path == "" {
		return
	}
	current := target[path]
	current.Path = path
	if current.Language == "" {
		current.Language = language
	}
	if confidence > current.Confidence {
		current.Confidence = confidence
	}
	current.Reasons = uniqueStrings(append(current.Reasons, reason))
	target[path] = current
}

func affectedMapToSortedList(values map[string]AffectedFile, limit int) []AffectedFile {
	items := make([]AffectedFile, 0, len(values))
	for _, item := range values {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Confidence == items[j].Confidence {
			return items[i].Path < items[j].Path
		}
		return items[i].Confidence > items[j].Confidence
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}
