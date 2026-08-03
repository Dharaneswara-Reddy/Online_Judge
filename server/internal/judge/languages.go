package judge

import "fmt"

// Language defines how to compile and run a specific programming language.
type Language struct {
	ID         string
	SourceFile string
	CompileCmd []string // nil for interpreted languages
	RunCmd     []string
}

var languages = map[string]Language{
	"c": {
		ID: "c", SourceFile: "main.c",
		CompileCmd: []string{"gcc", "main.c", "-o", "main", "-O2"},
		RunCmd:     []string{"./main"},
	},
	"cpp": {
		ID: "cpp", SourceFile: "main.cpp",
		CompileCmd: []string{"g++", "main.cpp", "-o", "main", "-O2", "-std=c++17"},
		RunCmd:     []string{"./main"},
	},
	"java": {
		ID: "java", SourceFile: "Main.java",
		CompileCmd: []string{"javac", "Main.java"},
		RunCmd:     []string{"java", "Main"},
	},
	"python": {
		ID: "python", SourceFile: "main.py",
		CompileCmd: nil,
		RunCmd:     []string{"python3", "main.py"},
	},
	"go": {
		ID: "go", SourceFile: "main.go",
		CompileCmd: []string{"go", "build", "-o", "main", "main.go"},
		RunCmd:     []string{"./main"},
	},
}

// GetLanguage returns the language config for the given ID, or an error
// if the language is not supported.
func GetLanguage(id string) (Language, error) {
	lang, ok := languages[id]
	if !ok {
		return Language{}, fmt.Errorf("unsupported language: %s", id)
	}
	return lang, nil
}
