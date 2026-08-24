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
		// The compile is wrapped in a shell so the pre-warmed build cache
		// can be copied into the writable tmpfs first.
		//
		// Pointing GOCACHE straight at the read-only /opt/gocache almost
		// works, and that is the trap: Go only rewrites the cache's
		// trim.txt about once a day, so it succeeds for a while and then
		// every Go submission starts failing with
		//   go: failed to trim cache: open /opt/gocache/trim.txt: permission denied
		// which surfaces to the user as a compile error in code that
		// compiles perfectly well. It would have started roughly a day
		// after the image was built.
		//
		// Copying costs about 250ms of the 15s compile budget and keeps
		// the whole benefit: a warm compile lands near 300ms against the
		// 12s a genuinely cold one takes under the judge's own limits.
		// The copy lands in the per-submission tmpfs, so one submission
		// still cannot influence another's build — which is exactly why
		// the shared copy is mounted read-only in the first place.
		CompileCmd: []string{"sh", "-c",
			"cp -r /opt/gocache /tmp/gocache 2>/dev/null; " +
				"GOCACHE=/tmp/gocache go build -buildvcs=false -o main main.go"},
		RunCmd: []string{"./main"},
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
