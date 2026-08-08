/**
 * Home.jsx — Authenticated landing page
 *
 * A minimal home page shown after the user logs in.
 * Displays the user's name and a logout button.
 * This is a placeholder that will be replaced with the
 * full problem list page in the next implementation phase.
 */

import { Link } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import './Home.css';

function Home() {
  const { user, logout } = useAuth();

  return (
    <div className="home-page">
      {/* Navigation bar */}
      <nav className="home-nav">
        <div className="home-nav-brand">
          <span style={{ fontSize: '1.2rem' }}>⚡</span>
          <span style={{ fontSize: '1.1rem', fontWeight: 700, color: 'var(--accent)', letterSpacing: '-0.02em' }}>CodeArena</span>
        </div>
        <div className="home-nav-right">
          <Link to="/problems" className="btn btn-ghost" style={{ padding: '7px 14px', fontSize: '0.8rem', textDecoration: 'none', borderColor: 'var(--accent)', color: 'var(--accent)' }}>
            Problems
          </Link>
          <Link to="/playground" className="btn btn-ghost" style={{ padding: '7px 14px', fontSize: '0.8rem', textDecoration: 'none' }}>
            ▶ Playground
          </Link>
          <span className="home-nav-user">
            {user?.full_name}
          </span>
          <button onClick={logout} className="btn btn-ghost" style={{ padding: '7px 14px', fontSize: '0.8rem' }}>
            Logout
          </button>
        </div>
      </nav>

      {/* Main content */}
      <main className="home-content">
        <div className="home-welcome-card card">
          <div className="home-welcome-emoji">🎯</div>
          <h1 className="home-welcome-title">
            Welcome, {user?.full_name?.split(' ')[0]}!
          </h1>
          <p className="home-welcome-text">
            Start solving problems from the <Link to="/problems" style={{ color: 'var(--accent)' }}>Problem List</Link>, or experiment freely in the <Link to="/playground" style={{ color: 'var(--accent)' }}>Playground</Link>.
            War Rooms and discussions are coming soon.
          </p>
          <div className="home-stats">
            <div className="home-stat">
              <span className="home-stat-value">0</span>
              <span className="home-stat-label">Problems Solved</span>
            </div>
            <div className="home-stat">
              <span className="home-stat-value">0</span>
              <span className="home-stat-label">Submissions</span>
            </div>
            <div className="home-stat">
              <span className="home-stat-value">0</span>
              <span className="home-stat-label">War Room Wins</span>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

export default Home;
