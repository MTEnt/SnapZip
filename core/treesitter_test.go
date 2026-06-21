package core

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestTreeSitterBasics(t *testing.T) {
	src := []byte(`
@my_decorator(param=1)
@another_decorator
def hello(name):
    print("hello " + name)

class MyClass:
    def method(self):
        pass
`)
	lang := grammars.PythonLanguage()
	if lang == nil {
		t.Fatal("Python language grammar is nil")
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	root := tree.RootNode()
	if root.ChildCount() != 2 {
		t.Fatalf("expected 2 children, got %d", root.ChildCount())
	}
	if root.Child(0).Type(lang) != "decorated_definition" {
		t.Fatalf("expected decorated_definition, got %s", root.Child(0).Type(lang))
	}
}

func TestJavaScriptExports(t *testing.T) {
	src := []byte(`
import { foo } from "./foo";

export default class MyClass {
    constructor() {
        this.foo = foo;
    }
}

export function test() {
    return 42;
}

const localVal = 10;
`)
	lang := grammars.JavascriptLanguage()
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	root := tree.RootNode()
	if root.ChildCount() != 4 {
		t.Fatalf("expected 4 children, got %d", root.ChildCount())
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		filename string
		expected string
	}{
		{"main.py", "python"},
		{"main.go", "go"},
		{"app.ts", "typescript"},
		{"app.tsx", "tsx"},
		{"helper.cpp", "cpp"},
		{"lib.rs", "rust"},
	}

	for _, tc := range cases {
		entry := grammars.DetectLanguage(tc.filename)
		if entry == nil {
			t.Fatalf("failed to detect language for %s", tc.filename)
		}
		if entry.Name != tc.expected {
			t.Fatalf("expected language %s for %s, got %s", tc.expected, tc.filename, entry.Name)
		}
		lang := entry.Language()
		if lang == nil {
			t.Fatalf("language grammar for %s is nil", tc.filename)
		}
	}
}
