/**
 * Home.jsx — the public landing page.
 *
 * Reachable without an account, as the design document intends: a hero,
 * live community statistics, and a preview of recently added problems,
 * so a visitor can see what the platform is before signing up.
 */

import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import NavBar from '../components/layout/NavBar';
import { fetchSummary, fetchRecentProblems } from '../api/stats';
import './Home.css';

function Home() {
  const { user } = useAuth();
  const [stats, setStats] = useState(null);
  const [recent, setRecent] = useState([]);

  useEffect(() => {
    // Both are decorative: a failure leaves the section out rather than
    // breaking the page a visitor first lands on.
    fetchSummary().then(setStats).catch(() => setStats(null));
    fetchRecentProblems(5).then(setRecent).catch(() => setRecent([]));
  }, []);

  return (
    <div className="home-page">
      <NavBar />

      <main className="home-content">
        <section className="home-hero">
          <h1 className="home-hero-title">
            Practice, then <span className="home-hero-accent">race</span>.
          </h1>
          <p className="home-hero-text">
            Solve problems against hidden test cases in a sandboxed judge, discuss
            approaches with other people, and challenge someone to a live head-to-head
            War Room whenever you feel like it.
          </p>

          <div className="home-hero-actions">
            <Link to="/problems" className="btn btn-primary home-cta">Browse problems</Link>
            <Link to={user ? '/warrooms' : '/signup'} className="btn btn-ghost home-cta">
              {user ? 'Start a War Room' : 'Create an account'}
            </Link>
          </div>
        </section>

        {stats && (
          <section className="home-stats">
            <StatTile value={stats.problems} label="Problems" />
            <StatTile value={stats.users} label="Coders" />
            <StatTile value={stats.submissions} label="Submissions" />
            <StatTile value={stats.problemsSolved} label="Solved" />
            <StatTile value={stats.activeWarRooms} label="Open war rooms" />
          </section>
        )}

        {recent.length > 0 && (
          <section className="home-recent">
            <div className="home-recent-head">
              <h2 className="home-section-title">Recently added</h2>
              <Link to="/problems" className="home-see-all">See all →</Link>
            </div>
            <ul className="home-recent-list">
              {recent.map((p) => (
                <li key={p.id}>
                  <Link to={`/problems/${p.slug}`} className="home-recent-item">
                    <span className="home-recent-title">{p.title}</span>
                    <span className={`badge badge-${p.difficulty}`}>{p.difficulty}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        )}

        <section className="home-features">
          <Feature
            icon="⚖️"
            title="Real judging"
            text="Every submission compiles and runs in a resource-capped, network-isolated container against the full hidden test set."
          />
          <Feature
            icon="⚔️"
            title="War Rooms"
            text="Two or three players, one problem, first accepted verdict wins. Judged on a dedicated priority lane so a race never waits."
          />
          <Feature
            icon="🏷️"
            title="Company tags"
            text="Tell others where you saw a problem in an interview, and browse problems by the companies that ask them."
          />
        </section>
      </main>
    </div>
  );
}

function StatTile({ value, label }) {
  return (
    <div className="home-stat">
      <span className="home-stat-value">{value ?? 0}</span>
      <span className="home-stat-label">{label}</span>
    </div>
  );
}

function Feature({ icon, title, text }) {
  return (
    <div className="card home-feature">
      <span className="home-feature-icon">{icon}</span>
      <h3 className="home-feature-title">{title}</h3>
      <p className="home-feature-text">{text}</p>
    </div>
  );
}

export default Home;
