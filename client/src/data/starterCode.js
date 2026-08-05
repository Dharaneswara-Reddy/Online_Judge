export const STARTER_CODE = {
  c: `#include <stdio.h>\n\nint main(void) {\n    printf("Hello, CodeArena!\\n");\n    return 0;\n}\n`,
  cpp: `#include <iostream>\n\nint main() {\n    std::cout << "Hello, CodeArena!" << std::endl;\n    return 0;\n}\n`,
  java: `public class Main {\n    public static void main(String[] args) {\n        System.out.println("Hello, CodeArena!");\n    }\n}\n`,
  python: `print("Hello, CodeArena!")\n`,
  go: `package main\n\nimport "fmt"\n\nfunc main() {\n    fmt.Println("Hello, CodeArena!")\n}\n`,
};

export const LANGUAGE_META = {
  c: { label: "C", monacoId: "c", ext: "c" },
  cpp: { label: "C++", monacoId: "cpp", ext: "cpp" },
  java: { label: "Java", monacoId: "java", ext: "java" },
  python: { label: "Python", monacoId: "python", ext: "py" },
  go: { label: "Go", monacoId: "go", ext: "go" },
};

export const LANGUAGE_ORDER = ["c", "cpp", "java", "python", "go"];
