import Editor, { loader } from "@monaco-editor/react";
import * as monaco from "monaco-editor";
import { LANGUAGE_META } from "../../data/starterCode";
import "./CodeEditor.css";

// Serve Monaco from our own bundle instead of jsdelivr.
//
// @monaco-editor/react fetches the editor from a CDN at runtime by
// default, which puts a third party on the critical path of the screen
// this product exists for: a blocked or slow CDN leaves the editor blank,
// and the version comes from a transitive default rather than our
// lockfile. Configuring the loader here rather than in the entry point
// keeps Monaco inside this module's chunk, so only the routes that
// actually mount an editor download it.
loader.config({ monaco });

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
