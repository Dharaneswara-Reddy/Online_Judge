/**
 * Profile.jsx — the signed-in user's account and solve record.
 *
 * Shows personal details with inline editing, a per-difficulty solve
 * breakdown, and the full submission history with filters.
 */

import { useState, useEffect, useCallback } from "react";
import { Link } from "react-router-dom";
import NavBar from "../components/layout/NavBar";
import { fetchProfile, updateProfile } from "../api/stats";
import { fetchMySubmissions } from "../api/submissions";
import "./Profile.css";

const DIFFICULTIES = ["easy", "medium", "hard"];

const STATUS_LABELS = {
  pending: "Queued",
  running: "Judging",
  accepted: "Accepted",
  wrong_answer: "Wrong Answer",
  tle: "Time Limit Exceeded",
  mle: "Memory Limit Exceeded",
  runtime_error: "Runtime Error",
  compile_error: "Compile Error",
  output_limit_exceeded: "Output Limit Exceeded",
  // Matches the wording ProblemDetail already uses: the judge failed,
  // and saying so is honest where "Runtime Error" would blame the code.
  error: "Could Not Judge",
};

const PAGE_SIZE = 10;

export default function Profile() {
  const [profile, setProfile] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  // Inline edit state for the personal details card.
  const [editing, setEditing] = useState(false);
  const [fullName, setFullName] = useState("");
  const [dob, setDob] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(null);

  // Submission history state.
  const [submissions, setSubmissions] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState("");

  const loadProfile = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await fetchProfile();
      setProfile(data);
      setFullName(data.user?.full_name ?? "");
      setDob(data.user?.dob ? data.user.dob.slice(0, 10) : "");
    } catch {
      setError("Could not load your profile.");
    } finally {
      setLoading(false);
    }
  }, []);

  const loadSubmissions = useCallback(async () => {
    try {
      const res = await fetchMySubmissions({
        status: statusFilter || undefined,
        page,
        pageSize: PAGE_SIZE,
      });
      setSubmissions(res.data ?? []);
      setTotal(res.total ?? 0);
    } catch {
      setSubmissions([]);
    }
  }, [page, statusFilter]);

  useEffect(() => { loadProfile(); }, [loadProfile]);
  useEffect(() => { loadSubmissions(); }, [loadSubmissions]);

  const handleSave = async (e) => {
    e.preventDefault();
    setSaving(true);
    setSaveError(null);
    try {
      await updateProfile({ full_name: fullName, dob: dob || undefined });
      setEditing(false);
      await loadProfile();
    } catch (err) {
      setSaveError(err?.response?.data?.message ?? "Could not save your changes.");
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="profile-page">
        <NavBar />
        <div className="profile-loading"><div className="spinner spinner-lg" /></div>
      </div>
    );
  }

  if (error || !profile) {
    return (
      <div className="profile-page">
        <NavBar />
        <div className="profile-empty">{error ?? "Profile unavailable."}</div>
      </div>
    );
  }

  const { user, stats } = profile;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const acceptanceRate = stats.totalSubmissions
    ? Math.round((stats.accepted / stats.totalSubmissions) * 100)
    : 0;

  return (
    <div className="profile-page">
      <NavBar />

      <main className="profile-main">
        <header className="profile-header">
          <div className="profile-avatar">{(user.username || "?").charAt(0).toUpperCase()}</div>
          <div>
            <h1 className="profile-name">{user.full_name || user.username}</h1>
            <p className="profile-meta">
              @{user.username} · {user.email}
              {user.role === "admin" && <span className="profile-role">admin</span>}
            </p>
          </div>
        </header>

        <section className="profile-grid">
          {/* Solve statistics */}
          <div className="card profile-card">
            <h2 className="profile-card-title">Solve record</h2>

            <div className="profile-stat-row">
              <Stat label="Solved" value={stats.solved} />
              <Stat label="Submissions" value={stats.totalSubmissions} />
              <Stat label="Accepted" value={stats.accepted} />
              <Stat label="Acceptance" value={`${acceptanceRate}%`} />
            </div>

            <div className="profile-bars">
              {DIFFICULTIES.map((level) => {
                const solved = stats.solvedByDifficulty?.[level] ?? 0;
                const available = stats.totalByDifficulty?.[level] ?? 0;
                const pct = available ? Math.round((solved / available) * 100) : 0;
                return (
                  <div key={level} className="profile-bar">
                    <div className="profile-bar-head">
                      <span className={`badge badge-${level}`}>{level}</span>
                      <span className="profile-bar-count">{solved} / {available}</span>
                    </div>
                    <div className="profile-bar-track">
                      <div className={`profile-bar-fill fill-${level}`} style={{ width: `${pct}%` }} />
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          {/* Personal details */}
          <div className="card profile-card">
            <div className="profile-card-head">
              <h2 className="profile-card-title">Details</h2>
              {!editing && (
                <button className="btn btn-ghost profile-edit-btn" onClick={() => setEditing(true)}>
                  Edit
                </button>
              )}
            </div>

            {editing ? (
              <form onSubmit={handleSave} className="profile-form">
                <label className="profile-field">
                  <span>Full name</span>
                  <input
                    className="form-input"
                    value={fullName}
                    onChange={(e) => setFullName(e.target.value)}
                    maxLength={100}
                  />
                </label>
                <label className="profile-field">
                  <span>Date of birth</span>
                  <input
                    className="form-input"
                    type="date"
                    value={dob}
                    onChange={(e) => setDob(e.target.value)}
                  />
                </label>

                {saveError && <p className="profile-error">{saveError}</p>}

                <div className="profile-form-actions">
                  <button type="submit" className="btn btn-primary" disabled={saving}>
                    {saving ? "Saving…" : "Save"}
                  </button>
                  <button
                    type="button"
                    className="btn btn-ghost"
                    onClick={() => { setEditing(false); setSaveError(null); }}
                    disabled={saving}
                  >
                    Cancel
                  </button>
                </div>
              </form>
            ) : (
              <dl className="profile-details">
                <Detail label="Username" value={user.username} />
                <Detail label="Email" value={user.email} />
                <Detail label="Full name" value={user.full_name || "—"} />
                <Detail label="Date of birth" value={user.dob ? user.dob.slice(0, 10) : "—"} />
                <Detail
                  label="Member since"
                  value={user.created_at ? new Date(user.created_at).toLocaleDateString() : "—"}
                />
              </dl>
            )}
          </div>
        </section>

        {/* Submission history */}
        <section className="card profile-card">
          <div className="profile-card-head">
            <h2 className="profile-card-title">Submissions</h2>
            <select
              className="form-input profile-filter"
              value={statusFilter}
              onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }}
            >
              <option value="">All verdicts</option>
              {Object.entries(STATUS_LABELS).map(([value, label]) => (
                <option key={value} value={value}>{label}</option>
              ))}
            </select>
          </div>

          {submissions.length === 0 ? (
            <p className="profile-empty-inline">
              No submissions yet. <Link to="/problems">Pick a problem</Link> to get started.
            </p>
          ) : (
            <>
              <div className="submission-table">
                <div className="submission-row submission-head">
                  <span>Problem</span>
                  <span>Verdict</span>
                  <span>Language</span>
                  <span>Runtime</span>
                  <span>When</span>
                </div>
                {submissions.map((s) => (
                  <div key={s.id} className="submission-row">
                    <Link to={`/problems/${s.problemSlug}`} className="submission-problem">
                      {s.problemTitle || s.problemSlug}
                    </Link>
                    <span className={`submission-verdict verdict-${s.status}`}>
                      {STATUS_LABELS[s.status] ?? s.status}
                    </span>
                    <span className="submission-muted">{s.language}</span>
                    <span className="submission-muted">
                      {s.status === "accepted" ? `${s.runtimeMs}ms` : "—"}
                    </span>
                    <span className="submission-muted">
                      {new Date(s.submittedAt).toLocaleString()}
                    </span>
                  </div>
                ))}
              </div>

              {totalPages > 1 && (
                <div className="profile-pagination">
                  <button
                    className="btn btn-ghost"
                    disabled={page <= 1}
                    onClick={() => setPage((p) => p - 1)}
                  >
                    ← Previous
                  </button>
                  <span className="profile-page-label">Page {page} of {totalPages}</span>
                  <button
                    className="btn btn-ghost"
                    disabled={page >= totalPages}
                    onClick={() => setPage((p) => p + 1)}
                  >
                    Next →
                  </button>
                </div>
              )}
            </>
          )}
        </section>
      </main>
    </div>
  );
}

function Stat({ label, value }) {
  return (
    <div className="profile-stat">
      <span className="profile-stat-value">{value}</span>
      <span className="profile-stat-label">{label}</span>
    </div>
  );
}

function Detail({ label, value }) {
  return (
    <div className="profile-detail">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}
