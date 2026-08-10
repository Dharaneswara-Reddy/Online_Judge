/**
 * Companies.jsx — the company explorer.
 *
 * One route serves both views: the index of companies, and the problems
 * tagged with a single company when :name is present in the URL.
 */

import { useState, useEffect } from "react";
import { useParams, Link } from "react-router-dom";
import NavBar from "../components/layout/NavBar";
import { fetchCompanies, fetchCompanyProblems } from "../api/companies";
import "./Companies.css";

export default function Companies() {
  const { name } = useParams();
  const [companies, setCompanies] = useState([]);
  const [problems, setProblems] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [search, setSearch] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    const request = name ? fetchCompanyProblems(name) : fetchCompanies();
    request
      .then((data) => {
        if (cancelled) return;
        if (name) setProblems(data);
        else setCompanies(data);
      })
      .catch(() => !cancelled && setError("Could not load company data."))
      .finally(() => !cancelled && setLoading(false));

    return () => { cancelled = true; };
  }, [name]);

  const visible = companies.filter((c) =>
    c.company.toLowerCase().includes(search.trim().toLowerCase())
  );

  return (
    <div className="companies-page">
      <NavBar />

      <main className="companies-main">
        {name ? (
          <>
            <header className="companies-header">
              <Link to="/companies" className="companies-back">← All companies</Link>
              <h1 className="companies-title">{name}</h1>
              <p className="companies-subtitle">
                Problems other users reported seeing in {name} interviews.
              </p>
            </header>

            {loading ? (
              <div className="companies-loading"><div className="spinner" /></div>
            ) : problems.length === 0 ? (
              <p className="companies-empty">No problems tagged with this company yet.</p>
            ) : (
              <ul className="companies-problem-list">
                {problems.map((p) => (
                  <li key={p.id}>
                    <Link to={`/problems/${p.slug}`} className="companies-problem">
                      <span className="companies-problem-title">{p.title}</span>
                      <span className={`badge badge-${p.difficulty}`}>{p.difficulty}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </>
        ) : (
          <>
            <header className="companies-header">
              <h1 className="companies-title">Companies</h1>
              <p className="companies-subtitle">
                Where the community has seen these problems in real interviews.
              </p>
              <input
                className="form-input companies-search"
                placeholder="Search companies…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </header>

            {error && <p className="companies-error">{error}</p>}

            {loading ? (
              <div className="companies-loading"><div className="spinner" /></div>
            ) : visible.length === 0 ? (
              <p className="companies-empty">
                {companies.length === 0
                  ? "No company tags yet. Add one from any problem page."
                  : "No companies match that search."}
              </p>
            ) : (
              <ul className="companies-grid">
                {visible.map((c) => (
                  <li key={c.company}>
                    <Link to={`/companies/${encodeURIComponent(c.company)}`} className="company-card">
                      <span className="company-card-name">{c.company}</span>
                      <span className="company-card-meta">
                        {c.problemCount} {c.problemCount === 1 ? "problem" : "problems"} · {c.tagCount} reports
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </main>
    </div>
  );
}
