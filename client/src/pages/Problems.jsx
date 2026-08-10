import { useState, useEffect, useCallback } from "react";
import { Link } from "react-router-dom";
import NavBar from "../components/layout/NavBar";
import { fetchProblems } from "../api/problems";
import "./Problems.css";

const DIFFICULTIES = ["", "easy", "medium", "hard"];
const DIFFICULTY_LABELS = { "": "All", easy: "Easy", medium: "Medium", hard: "Hard" };
const DIFFICULTY_CLASS = { easy: "badge-easy", medium: "badge-medium", hard: "badge-hard" };

export default function Problems() {
  const [problems, setProblems] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [difficulty, setDifficulty] = useState("");
  const [page, setPage] = useState(1);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetchProblems({ difficulty: difficulty || undefined, page });
      setProblems(res.data || []);
    } catch {
      setError("Failed to load problems.");
    } finally {
      setLoading(false);
    }
  }, [difficulty, page]);

  useEffect(() => { load(); }, [load]);

  const handleDifficultyChange = (d) => {
    setDifficulty(d);
    setPage(1);
  };

  return (
    <div className="problems-page">
      <NavBar />

      <main className="problems-content">
        <div className="problems-header">
          <h1 className="problems-title">Problems</h1>
          <div className="problems-filters">
            {DIFFICULTIES.map((d) => (
              <button
                key={d}
                className={`filter-btn ${difficulty === d ? "filter-btn--active" : ""}`}
                onClick={() => handleDifficultyChange(d)}
              >
                {DIFFICULTY_LABELS[d]}
              </button>
            ))}
          </div>
        </div>

        {loading && (
          <div className="problems-loading">
            <div className="spinner spinner-lg" />
          </div>
        )}

        {error && <div className="alert alert-error">{error}</div>}

        {!loading && !error && (
          <>
            <div className="problems-table-wrap">
              <table className="problems-table">
                <thead>
                  <tr>
                    <th className="th-num">#</th>
                    <th className="th-title">Title</th>
                    <th className="th-difficulty">Difficulty</th>
                    <th className="th-tags">Tags</th>
                    <th className="th-acceptance">Acceptance</th>
                  </tr>
                </thead>
                <tbody>
                  {problems.length === 0 && (
                    <tr>
                      <td colSpan={5} className="problems-empty">No problems found.</td>
                    </tr>
                  )}
                  {problems.map((p, i) => (
                    <tr key={p.id} className="problem-row">
                      <td className="td-num">{(page - 1) * 20 + i + 1}</td>
                      <td className="td-title">
                        <Link to={`/problems/${p.slug}`} className="problem-link">{p.title}</Link>
                      </td>
                      <td className="td-difficulty">
                        <span className={`difficulty-badge ${DIFFICULTY_CLASS[p.difficulty] || ""}`}>
                          {p.difficulty?.charAt(0).toUpperCase() + p.difficulty?.slice(1)}
                        </span>
                      </td>
                      <td className="td-tags">
                        {(p.tags || []).map((tag) => (
                          <span key={tag} className="tag-pill">{tag}</span>
                        ))}
                      </td>
                      <td className="td-acceptance">—</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="problems-pagination">
              <button
                className="btn btn-ghost pagination-btn"
                disabled={page <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                ← Prev
              </button>
              <span className="pagination-info">Page {page}</span>
              <button
                className="btn btn-ghost pagination-btn"
                disabled={problems.length < 20}
                onClick={() => setPage((p) => p + 1)}
              >
                Next →
              </button>
            </div>
          </>
        )}
      </main>
    </div>
  );
}
