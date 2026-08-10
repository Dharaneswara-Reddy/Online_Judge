/**
 * CompanyTagWidget.jsx — "Have you seen this in an interview?"
 *
 * Collects one answer per company from each user and shows the
 * aggregated result. Companies the user already reported are listed back
 * to them so the widget stops asking the same question.
 */

import { useState, useEffect, useCallback } from "react";
import { Link } from "react-router-dom";
import { fetchCompanyTags, tagCompany } from "../../api/companies";
import "./CompanyTagWidget.css";

const ROUNDS = [
  { value: "", label: "Not sure" },
  { value: "oa", label: "Online assessment" },
  { value: "phone screen", label: "Phone screen" },
  { value: "onsite", label: "Onsite" },
  { value: "final", label: "Final round" },
];

export default function CompanyTagWidget({ slug, signedIn }) {
  const [companies, setCompanies] = useState([]);
  const [myTags, setMyTags] = useState([]);
  const [company, setCompany] = useState("");
  const [round, setRound] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [open, setOpen] = useState(false);

  const load = useCallback(async () => {
    try {
      const { companies, myTags } = await fetchCompanyTags(slug);
      setCompanies(companies);
      setMyTags(myTags);
    } catch {
      setCompanies([]);
    }
  }, [slug]);

  useEffect(() => { load(); }, [load]);

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!company.trim() || saving) return;
    setSaving(true);
    setError(null);
    try {
      await tagCompany(slug, { company, round });
      setCompany("");
      setRound("");
      setOpen(false);
      await load();
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not save your tag.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="company-widget">
      <h3 className="company-widget-title">Seen in interviews at</h3>

      {companies.length === 0 ? (
        <p className="company-widget-empty">No reports yet.</p>
      ) : (
        <ul className="company-chips">
          {companies.map((c) => (
            <li key={c.company}>
              <Link to={`/companies/${encodeURIComponent(c.company)}`} className="company-chip">
                {c.company}
                <span className="company-chip-count">{c.tagCount}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}

      {myTags.length > 0 && (
        <p className="company-widget-mine">
          You reported: {myTags.map((t) => t.company).join(", ")}
        </p>
      )}

      {signedIn ? (
        open ? (
          <form onSubmit={handleSubmit} className="company-form">
            <input
              className="form-input company-input"
              placeholder="Company name"
              value={company}
              onChange={(e) => setCompany(e.target.value)}
              maxLength={64}
              autoFocus
            />
            <select
              className="form-input company-input"
              value={round}
              onChange={(e) => setRound(e.target.value)}
            >
              {ROUNDS.map((r) => (
                <option key={r.value} value={r.value}>{r.label}</option>
              ))}
            </select>
            <div className="company-form-actions">
              <button type="submit" className="btn btn-primary company-btn" disabled={saving || !company.trim()}>
                {saving ? "Saving…" : "Add"}
              </button>
              <button type="button" className="btn btn-ghost company-btn" onClick={() => { setOpen(false); setError(null); }}>
                Cancel
              </button>
            </div>
            {error && <p className="company-error">{error}</p>}
          </form>
        ) : (
          <button className="btn btn-ghost company-btn company-ask" onClick={() => setOpen(true)}>
            + Have you seen this in an interview?
          </button>
        )
      ) : (
        <p className="company-widget-empty">Sign in to report where you saw this problem.</p>
      )}
    </section>
  );
}
