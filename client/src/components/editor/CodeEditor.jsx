import Editor from "@monaco-editor/react";
import { LANGUAGE_META } from "../../data/starterCode";
import "./CodeEditor.css";

function defineTheme(monaco) {
  monaco.editor.defineTheme("codearena-dark", {
    base: "vs-dark",
    inherit: true,
    rules: [],
    colors: {
      "editor.background": "#101115",
      "editor.foreground": "#ece9e2",
      "editorLineNumber.foreground": "#4b4d55",
      "editorLineNumber.activeForeground": "#e3a857",
      "editorCursor.foreground": "#e3a857",
      "editor.selectionBackground": "#e3a85733",
      "editor.lineHighlightBackground": "#17181d",
      "editorGutter.background": "#101115",
    },
  });
}

export default function CodeEditor({ language, value, onChange, readOnly = false }) {
  return (
    <div className="code-editor-wrap">
      <Editor
        height="100%"
        language={LANGUAGE_META[language]?.monacoId ?? "plaintext"}
        value={value}
        onChange={(v) => onChange(v ?? "")}
        theme="codearena-dark"
        beforeMount={defineTheme}
        options={{
          fontFamily: "'Fira Code', monospace",
          fontSize: 14,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          readOnly,
          automaticLayout: true,
          padding: { top: 16 },
          tabSize: 4,
        }}
      />
    </div>
  );
}
