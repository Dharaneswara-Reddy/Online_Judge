package judge

import "testing"

func TestGetLanguage(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		wantFile string
		wantErr  bool
	}{
		{"c", "c", "main.c", false},
		{"cpp", "cpp", "main.cpp", false},
		{"java", "java", "Main.java", false},
		{"python", "python", "main.py", false},
		{"go", "go", "main.go", false},
		{"unsupported", "ruby", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lang, err := GetLanguage(tc.id)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if lang.SourceFile != tc.wantFile {
				t.Errorf("got file %q, want %q", lang.SourceFile, tc.wantFile)
			}
		})
	}
}

// Java's public class must be named Main to match the filename. This is
// enforced by locking the frontend editor's boilerplate to
// `public class Main { ... }`, not by parsing/renaming submitted code —
// this test just documents and pins that assumption at the config level.
func TestJavaRequiresPublicClassMain(t *testing.T) {
	lang, err := GetLanguage("java")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang.SourceFile != "Main.java" {
		t.Errorf("java source file must be Main.java, got %q", lang.SourceFile)
	}
}
