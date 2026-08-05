import "./ExecutionPanel.css";

const STATUS_LABEL = {
  idle: null,
  running: "Running",
  success: "Success",
  runtime_error: "Runtime Error",
  tle: "Time Limit Exceeded",
  mle: "Memory Limit Exceeded",
};

function deriveStatus(result) {
  if (!result) return "idle";
  if (result.timedOut) return "tle";
  if (result.oomKilled) return "mle";
  if (result.exitCode !== 0) return "runtime_error";
  return "success";
}

export default function ExecutionPanel({ stdin, onStdinChange, isRunning, result, activeTab, onTabChange }) {
  const status = isRunning ? "running" : deriveStatus(result);

  return (
    <div className="exec-panel">
      <div className="exec-tabs" role="tablist">
        <button role="tab" aria-selected={activeTab === "input"}
          className={`exec-tab ${activeTab === "input" ? "exec-tab--active" : ""}`}
          onClick={() => onTabChange("input")}>input</button>
        <button role="tab" aria-selected={activeTab === "output"}
          className={`exec-tab ${activeTab === "output" ? "exec-tab--active" : ""}`}
          onClick={() => onTabChange("output")}>output</button>
        {status !== "idle" && (
          <span className={`exec-status exec-status--${status}`}>{STATUS_LABEL[status]}</span>
        )}
      </div>

      <div className="exec-body">
        {activeTab === "input" ? (
          <textarea className="exec-input" placeholder="stdin (optional)"
            value={stdin} onChange={(e) => onStdinChange(e.target.value)} spellCheck={false} />
        ) : (
          <div className="exec-output">
            {status === "idle" && <p className="exec-placeholder">Output will appear here after you run your code.</p>}
            {status === "running" && <p className="exec-placeholder exec-placeholder--pulse">Compiling &amp; running&hellip;</p>}
            {status !== "idle" && status !== "running" && (
              <>
                {result.stdout && <pre className="exec-stdout">{result.stdout}</pre>}
                {result.stderr && <pre className="exec-stderr">{result.stderr}</pre>}
                {!result.stdout && !result.stderr && <p className="exec-placeholder">Program produced no output.</p>}
                <div className="exec-meta">
                  <span>exit code: {result.exitCode}</span>
                  {typeof result.runtimeMs === "number" && <span>runtime: {result.runtimeMs}ms</span>}
                </div>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
