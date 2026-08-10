import { useState, useEffect, useCallback } from "react";
import { useParams, Link } from "react-router-dom";
import { useAuth } from "../context/AuthContext";
import { fetchProblem } from "../api/problems";
import { submitSolution, pollSubmission, isTerminalStatus } from "../api/submissions";
import { runCode } from "../api/judge";
import CodeEditor from "../components/editor/CodeEditor";
import LanguageSelector from "../components/editor/LanguageSelector";
import ExecutionPanel from "../components/editor/ExecutionPanel";
import { STARTER_CODE, LANGUAGE_META } from "../data/starterCode";
import "./ProblemDetail.css";

const DIFFICULTY_CLASS = { easy: "badge-easy", medium: "badge-medium", hard: "badge-hard" };

const VERDICT_DISPLAY = {
  // Transient states, shown while the submission waits for a judge worker.
  pending: { label: "Queued", class: "verdict-pending", icon: "⋯" },
  running: { label: "Judging", class: "verdict-pending", icon: "⋯" },
  accepted: { label: "Accepted", class: "verdict-accepted", icon: "✓" },
  wrong_answer: { label: "Wrong Answer", class: "verdict-wa", icon: "✗" },
  tle: { label: "Time Limit Exceeded", class: "verdict-tle", icon: "⏱" },
  mle: { label: "Memory Limit Exceeded", class: "verdict-mle", icon: "📦" },
  runtime_error: { label: "Runtime Error", class: "verdict-re", icon: "💥" },
  compile_error: { label: "Compile Error", class: "verdict-ce", icon: "⚠" },
};

export default function ProblemDetail() {
  const { slug } = useParams();
  const { user, logout } = useAuth();

  const [problem, setProblem] = useState(null);
  const [sampleCases, setSampleCases] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  // Editor state
  const [language, setLanguage] = useState("python");
  const [codeByLanguage, setCodeByLanguage] = useState({ ...STARTER_CODE });

  // Execution state (Run)
  const [stdin, setStdin] = useState("");
  const [activeTab, setActiveTab] = useState("input");
  const [isRunning, setIsRunning] = useState(false);
  const [runResult, setRunResult] = useState(null);

  // Submission state
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitResult, setSubmitResult] = useState(null);

  // Fetch problem data
  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const res = await fetchProblem(slug);
        if (!cancelled) {
          setProblem(res.data);
          setSampleCases(res.sampleCases || []);

          // Use problem's starter code if available, merge with defaults
          if (res.data?.starterCode) {
            setCodeByLanguage((prev) => {
              const merged = { ...prev };
              for (const [lang, code] of Object.entries(res.data.starterCode)) {
                merged[lang] = code;
              }
              return merged;
            });
          }

          // Pre-fill stdin with first sample input
          if (res.sampleCases?.length > 0) {
            setStdin(res.sampleCases[0].input);
          }
        }
      } catch {
        if (!cancelled) setError("Failed to load problem.");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [slug]);

  const code = codeByLanguage[language] || "";

  const handleCodeChange = useCallback((value) => {
    setCodeByLanguage((prev) => ({ ...prev, [language]: value }));
  }, [language]);

  // Run — uses /api/judge/run-raw (same as Playground)
  const handleRun = async () => {
    if (isRunning || isSubmitting) return;
    setIsRunning(true);
    setRunResult(null);
    setSubmitResult(null);
    setActiveTab("output");
    try {
      const data = await runCode({ language, code, stdin });
      setRunResult(data);
    } catch (err) {
      setRunResult({ stdout: "", stderr: err?.response?.data?.message ?? "Execution failed.", exitCode: 1 });
    } finally {
      setIsRunning(false);
    }
  };

  // Submit — queues the solution, then polls until the judge decides.
  // The API returns as soon as the attempt is recorded, so a verdict
  // arrives on a later poll rather than in the POST response.
  const handleSubmit = async () => {
    if (isRunning || isSubmitting) return;
    setIsSubmitting(true);
    setSubmitResult(null);
    setRunResult(null);
    setActiveTab("output");
    try {
      const queued = await submitSolution({ slug, language, code });
      setSubmitResult(queued);

      if (queued.submissionId && !isTerminalStatus(queued.status)) {
        const final = await pollSubmission(queued.submissionId, {
          onUpdate: setSubmitResult,
        });
        setSubmitResult(final);
      }
    } catch (err) {
      setSubmitResult({
        status: "runtime_error",
        compileError: err?.response?.data?.message ?? "Submission failed.",
      });
    } finally {
      setIsSubmitting(false);
    }
  };

  if (loading) {
    return (
      <div className="pd-page">
        <div className="pd-loading"><div className="spinner spinner-lg" /></div>
      </div>
    );
  }

  if (error || !problem) {
    return (
      <div className="pd-page">
        <div className="pd-error">
          <p>{error || "Problem not found."}</p>
          <Link to="/problems" className="btn btn-primary">← Back to Problems</Link>
        </div>
      </div>
    );
  }

  const verdictInfo = submitResult ? VERDICT_DISPLAY[submitResult.status] : null;

  return (
    <div className="pd-page">
      {/* Navigation */}
      <nav className="home-nav">
        <div className="home-nav-brand">
          <span style={{ fontSize: "1.2rem" }}>⚡</span>
          <Link to="/" style={{ fontSize: "1.1rem", fontWeight: 700, color: "var(--accent)", letterSpacing: "-0.02em", textDecoration: "none" }}>
            CodeArena
          </Link>
        </div>
        <div className="home-nav-right">
          <Link to="/problems" className="btn btn-ghost" style={{ padding: "7px 14px", fontSize: "0.8rem", textDecoration: "none" }}>
            ← Problems
          </Link>
          <span className="home-nav-user">{user?.full_name}</span>
          <button onClick={logout} className="btn btn-ghost" style={{ padding: "7px 14px", fontSize: "0.8rem" }}>
            Logout
          </button>
        </div>
      </nav>

      {/* Split pane */}
      <div className="pd-split">
        {/* Left pane — Problem statement */}
        <div className="pd-left">
          <div className="pd-statement-card">
            <div className="pd-meta-row">
              <span className={`difficulty-badge ${DIFFICULTY_CLASS[problem.difficulty] || ""}`}>
                {problem.difficulty?.charAt(0).toUpperCase() + problem.difficulty?.slice(1)}
              </span>
              {(problem.tags || []).map((t) => (
                <span key={t} className="tag-pill">{t}</span>
              ))}
            </div>

            <h1 className="pd-title">{problem.title}</h1>

            <div className="pd-constraints">
              <span className="pd-constraint">⏱ {problem.timeLimitMs}ms</span>
              <span className="pd-constraint">📦 {problem.memoryLimitMb}MB</span>
            </div>

            <div className="pd-statement-text">
              {problem.statement.split("\n").map((line, i) => (
                <p key={i} className={line.trim() === "" ? "pd-blank" : ""}>{line || "\u00A0"}</p>
              ))}
            </div>

            {/* Sample test cases */}
            {sampleCases.length > 0 && (
              <div className="pd-samples">
                <h3 className="pd-samples-heading">Examples</h3>
                {sampleCases.map((tc, idx) => (
                  <div key={tc.id} className="pd-sample">
                    <div className="pd-sample-label">Example {idx + 1}</div>
                    <div className="pd-io-grid">
                      <div className="pd-io-block">
                        <div className="pd-io-label">Input</div>
                        <pre className="pd-io-pre">{tc.input}</pre>
                      </div>
                      <div className="pd-io-block">
                        <div className="pd-io-label">Output</div>
                        <pre className="pd-io-pre">{tc.expectedOutput}</pre>
                      </div>
                    </div>
                    <button
                      className="pd-use-input-btn"
                      onClick={() => { setStdin(tc.input); setActiveTab("input"); }}
                      title="Use this input in the editor"
                    >
                      Use Input ↗
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* Right pane — Editor + Execution */}
        <div className="pd-right">
          <div className="pd-editor-wrap">
            <div className="pd-toolbar">
              <LanguageSelector value={language} onChange={setLanguage} disabled={isRunning || isSubmitting} />
              <div className="pd-toolbar-actions">
                <button className="run-btn" onClick={handleRun} disabled={isRunning || isSubmitting}>
                  {isRunning ? "Running…" : "▶ Run"}
                </button>
                <button className="submit-btn" onClick={handleSubmit} disabled={isRunning || isSubmitting}>
                  {isSubmitting ? "Judging…" : "⬆ Submit"}
                </button>
              </div>
            </div>
            <div className="pd-code-area">
              <CodeEditor language={language} value={code} onChange={handleCodeChange} />
            </div>
          </div>

          {/* Verdict banner */}
          {submitResult && verdictInfo && (
            <div className={`pd-verdict-banner ${verdictInfo.class}`}>
              <span className="pd-verdict-icon">{verdictInfo.icon}</span>
              <span className="pd-verdict-label">{verdictInfo.label}</span>
              {submitResult.status === "accepted" && (
                <span className="pd-verdict-detail">
                  All {submitResult.totalCases} test cases passed • {submitResult.runtimeMs}ms
                </span>
              )}
              {submitResult.status === "wrong_answer" && (
                <span className="pd-verdict-detail">
                  Failed on test case {submitResult.failedCase + 1} of {submitResult.totalCases}
                </span>
              )}
              {submitResult.status === "tle" && (
                <span className="pd-verdict-detail">
                  Exceeded time limit on test case {submitResult.failedCase + 1}
                </span>
              )}
              {submitResult.status === "compile_error" && (
                <span className="pd-verdict-detail">{submitResult.compileError}</span>
              )}
              {submitResult.status === "runtime_error" && (
                <span className="pd-verdict-detail">
                  Runtime error on test case {submitResult.failedCase + 1}
                </span>
              )}
            </div>
          )}

          {/* Execution panel (stdin/stdout for Run) */}
          <div className="pd-exec-panel">
            <ExecutionPanel
              stdin={stdin}
              onStdinChange={setStdin}
              isRunning={isRunning || isSubmitting}
              result={runResult}
              activeTab={activeTab}
              onTabChange={setActiveTab}
            />
          </div>
        </div>
      </div>
    </div>
  );
}
