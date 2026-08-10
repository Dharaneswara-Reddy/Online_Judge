/**
 * Admin.jsx — the admin dashboard.
 *
 * Creates and edits problems and their test cases. Every endpoint it
 * calls is admin-gated on the server; the role check here only decides
 * what to render, never what is permitted.
 */

import { useState, useEffect, useCallback } from "react";
import { Navigate } from "react-router-dom";
import { useAuth } from "../context/AuthContext";
import NavBar from "../components/layout/NavBar";
import { fetchProblems } from "../api/problems";
import {
  createProblem,
  updateProblem,
  fetchTestCases,
  addTestCase,
} from "../api/admin";
import "./Admin.css";

const EMPTY_PROBLEM = {
  title: "",
  statement: "",
  difficulty: "easy",
  tags: "",
  timeLimitMs: 1000,
  memoryLimitMb: 128,
};

export default function Admin() {
  const { user } = useAuth();

  const [problems, setProblems] = useState([]);
  const [selected, setSelected] = useState(null);
  const [form, setForm] = useState(EMPTY_PROBLEM);
  const [testCases, setTestCases] = useState([]);
  const [message, setMessage] = useState(null);
  const [error, setError] = useState(null);
  const [saving, setSaving] = useState(false);

  const [newCase, setNewCase] = useState({ input: "", expectedOutput: "", isSample: false });

  const loadProblems = useCallback(async () => {
    try {
      const res = await fetchProblems({ pageSize: 100 });
      setProblems(res.data ?? []);
    } catch {
      setError("Could not load problems.");
    }
  }, []);

  useEffect(() => { loadProblems(); }, [loadProblems]);

  // Non-admins never see this page; the server would reject them anyway.
  if (user && user.role !== "admin") {
    return <Navigate to="/" replace />;
  }

  const selectProblem = async (problem) => {
    setSelected(problem);
    setError(null);
    setMessage(null);
    setForm({
      title: problem.title,
      statement: problem.statement,
      difficulty: problem.difficulty,
      tags: (problem.tags ?? []).join(", "),
      timeLimitMs: problem.timeLimitMs,
      memoryLimitMb: problem.memoryLimitMb,
    });
    try {
      setTestCases(await fetchTestCases(problem.id));
    } catch {
      setTestCases([]);
    }
  };

  const startNew = () => {
    setSelected(null);
    setForm(EMPTY_PROBLEM);
    setTestCases([]);
    setMessage(null);
    setError(null);
  };

  const handleSave = async (e) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    setMessage(null);

    const payload = {
      title: form.title,
      statement: form.statement,
      difficulty: form.difficulty,
      tags: form.tags.split(",").map((t) => t.trim()).filter(Boolean),
      timeLimitMs: Number(form.timeLimitMs),
      memoryLimitMb: Number(form.memoryLimitMb),
    };

    try {
      const saved = selected
        ? await updateProblem(selected.id, payload)
        : await createProblem(payload);
      setMessage(selected ? "Problem updated." : "Problem created.");
      await loadProblems();
      await selectProblem(saved);
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not save the problem.");
    } finally {
      setSaving(false);
    }
  };

  const handleAddCase = async (e) => {
    e.preventDefault();
    if (!selected) return;
    setError(null);
    try {
      await addTestCase(selected.id, newCase);
      setNewCase({ input: "", expectedOutput: "", isSample: false });
      setTestCases(await fetchTestCases(selected.id));
      setMessage("Test case added.");
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not add the test case.");
    }
  };

  return (
    <div className="admin-page">
      <NavBar />

      <main className="admin-main">
        <header className="admin-header">
          <h1 className="admin-title">Admin</h1>
          <p className="admin-subtitle">Manage problems and their test cases.</p>
        </header>

        {message && <p className="admin-message">{message}</p>}
        {error && <p className="admin-error">{error}</p>}

        <div className="admin-layout">
          {/* Problem list */}
          <aside className="card admin-list">
            <div className="admin-list-head">
              <h2 className="admin-card-title">Problems</h2>
              <button className="btn btn-primary admin-small-btn" onClick={startNew}>+ New</button>
            </div>
            <ul className="admin-problems">
              {problems.map((p) => (
                <li key={p.id}>
                  <button
                    className={`admin-problem ${selected?.id === p.id ? "admin-problem-active" : ""}`}
                    onClick={() => selectProblem(p)}
                  >
                    <span>{p.title}</span>
                    <span className={`badge badge-${p.difficulty}`}>{p.difficulty}</span>
                  </button>
                </li>
              ))}
            </ul>
          </aside>

          <div className="admin-editor">
            {/* Problem form */}
            <form onSubmit={handleSave} className="card admin-card">
              <h2 className="admin-card-title">
                {selected ? `Edit: ${selected.title}` : "New problem"}
              </h2>

              <label className="admin-field">
                <span>Title</span>
                <input
                  className="form-input"
                  value={form.title}
                  onChange={(e) => setForm({ ...form, title: e.target.value })}
                  required
                />
              </label>

              <label className="admin-field">
                <span>Statement</span>
                <textarea
                  className="form-input admin-textarea"
                  rows={8}
                  value={form.statement}
                  onChange={(e) => setForm({ ...form, statement: e.target.value })}
                  required
                />
              </label>

              <div className="admin-row">
                <label className="admin-field">
                  <span>Difficulty</span>
                  <select
                    className="form-input"
                    value={form.difficulty}
                    onChange={(e) => setForm({ ...form, difficulty: e.target.value })}
                  >
                    <option value="easy">Easy</option>
                    <option value="medium">Medium</option>
                    <option value="hard">Hard</option>
                  </select>
                </label>

                <label className="admin-field">
                  <span>Time limit (ms)</span>
                  <input
                    className="form-input"
                    type="number"
                    min={100}
                    value={form.timeLimitMs}
                    onChange={(e) => setForm({ ...form, timeLimitMs: e.target.value })}
                  />
                </label>

                <label className="admin-field">
                  <span>Memory limit (MB)</span>
                  <input
                    className="form-input"
                    type="number"
                    min={16}
                    value={form.memoryLimitMb}
                    onChange={(e) => setForm({ ...form, memoryLimitMb: e.target.value })}
                  />
                </label>
              </div>

              <label className="admin-field">
                <span>Tags (comma separated)</span>
                <input
                  className="form-input"
                  value={form.tags}
                  onChange={(e) => setForm({ ...form, tags: e.target.value })}
                  placeholder="array, hash-map"
                />
              </label>

              <button type="submit" className="btn btn-primary" disabled={saving}>
                {saving ? "Saving…" : selected ? "Save changes" : "Create problem"}
              </button>
            </form>

            {/* Test cases */}
            {selected && (
              <section className="card admin-card">
                <h2 className="admin-card-title">Test cases ({testCases.length})</h2>

                {testCases.length === 0 ? (
                  <p className="admin-empty">
                    No test cases yet. A problem without them can never be accepted.
                  </p>
                ) : (
                  <ul className="admin-cases">
                    {testCases.map((tc, i) => (
                      <li key={tc.id} className="admin-case">
                        <div className="admin-case-head">
                          <span>Case {i + 1}</span>
                          <span className={tc.isSample ? "admin-sample" : "admin-hidden"}>
                            {tc.isSample ? "sample" : "hidden"}
                          </span>
                        </div>
                        <div className="admin-case-io">
                          <pre className="admin-pre">{tc.input || "(no input)"}</pre>
                          <pre className="admin-pre">{tc.expectedOutput}</pre>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}

                <form onSubmit={handleAddCase} className="admin-case-form">
                  <div className="admin-row">
                    <label className="admin-field">
                      <span>Input</span>
                      <textarea
                        className="form-input admin-mono"
                        rows={3}
                        value={newCase.input}
                        onChange={(e) => setNewCase({ ...newCase, input: e.target.value })}
                      />
                    </label>
                    <label className="admin-field">
                      <span>Expected output</span>
                      <textarea
                        className="form-input admin-mono"
                        rows={3}
                        value={newCase.expectedOutput}
                        onChange={(e) => setNewCase({ ...newCase, expectedOutput: e.target.value })}
                        required
                      />
                    </label>
                  </div>
                  <label className="admin-checkbox">
                    <input
                      type="checkbox"
                      checked={newCase.isSample}
                      onChange={(e) => setNewCase({ ...newCase, isSample: e.target.checked })}
                    />
                    Show as a sample on the problem page
                  </label>
                  <button type="submit" className="btn btn-primary admin-small-btn">Add test case</button>
                </form>
              </section>
            )}
          </div>
        </div>
      </main>
    </div>
  );
}
