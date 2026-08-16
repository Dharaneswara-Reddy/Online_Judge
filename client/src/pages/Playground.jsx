import { lazy, Suspense, useState, useCallback } from "react";
import TerminalChrome from "../components/ui/TerminalChrome";
import LanguageSelector from "../components/editor/LanguageSelector";
import ExecutionPanel from "../components/editor/ExecutionPanel";
import { runCode } from "../api/judge";
import { STARTER_CODE, LANGUAGE_META } from "../data/starterCode";
import "./Playground.css";

// Monaco loads on demand rather than with the initial bundle.
const CodeEditor = lazy(() => import("../components/editor/CodeEditor"));
const EditorFallback = () => <div className="editor-loading"><div className="spinner" /></div>;

export default function Playground() {
  const [language, setLanguage] = useState("python");
  const [codeByLanguage, setCodeByLanguage] = useState(STARTER_CODE);
  const [stdin, setStdin] = useState("");
  const [activeTab, setActiveTab] = useState("input");
  const [isRunning, setIsRunning] = useState(false);
  const [result, setResult] = useState(null);
  const [runError, setRunError] = useState(null);

  const code = codeByLanguage[language];

  const handleCodeChange = useCallback((value) => {
    setCodeByLanguage((prev) => ({ ...prev, [language]: value }));
  }, [language]);

  const handleRun = async () => {
    if (isRunning) return;
    setIsRunning(true);
    setRunError(null);
    setActiveTab("output");
    try {
      const data = await runCode({ language, code, stdin });
      setResult(data);
    } catch (err) {
      setRunError(err?.response?.data?.message ?? "Something went wrong running your code.");
    } finally {
      setIsRunning(false);
    }
  };

  return (
    <div className="playground-page">
      <div className="playground-frame">
        <TerminalChrome path={`~/playground/run.${LANGUAGE_META[language].ext}`} />
        <div className="playground-body">
          <div className="playground-editor-col">
            <div className="playground-toolbar">
              <LanguageSelector value={language} onChange={setLanguage} disabled={isRunning} />
              <button className="run-btn" onClick={handleRun} disabled={isRunning}>
                {isRunning ? "Running\u2026" : "\u25B6 Run"}
              </button>
            </div>
            <Suspense fallback={<EditorFallback />}><CodeEditor language={language} value={code} onChange={handleCodeChange} /></Suspense>
          </div>
          <div className="playground-panel-col">
            <ExecutionPanel
              stdin={stdin}
              onStdinChange={setStdin}
              isRunning={isRunning}
              result={runError ? { stdout: "", stderr: runError, exitCode: 1 } : result}
              activeTab={activeTab}
              onTabChange={setActiveTab}
            />
          </div>
        </div>
      </div>
    </div>
  );
}
