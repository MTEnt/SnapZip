package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectConfigLoadsValidationCommands(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".snapzip", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`
[validation]
command = "go test ./..."

[validation.commands]
go = "go test ./pkg"
py = "python -m pytest"
`), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Found || config.Validation.Command != "go test ./..." {
		t.Fatalf("unexpected config: %+v", config)
	}

	report := AffectedReport{
		InputPaths: []string{"pkg/cache.go"},
		Tests:      []AffectedFile{{Path: "pkg/cache_test.go", Language: "go"}},
	}
	commands := ConfiguredValidationCommands(config, report)
	if len(commands) != 2 {
		t.Fatalf("got %d configured commands, want 2: %+v", len(commands), commands)
	}
	if commands[0].Command != "go test ./pkg" || commands[1].Command != "go test ./..." {
		t.Fatalf("configured commands not ordered by specificity: %+v", commands)
	}
}

func TestWriteDefaultProjectConfigSkipsExisting(t *testing.T) {
	root := t.TempDir()
	path, written, err := WriteDefaultProjectConfig(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected first config write")
	}

	pathAgain, writtenAgain, err := WriteDefaultProjectConfig(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if writtenAgain || pathAgain != path {
		t.Fatalf("expected existing config to be skipped, got written=%v path=%s", writtenAgain, pathAgain)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[validation]") {
		t.Fatalf("default config missing validation section:\n%s", data)
	}
}
