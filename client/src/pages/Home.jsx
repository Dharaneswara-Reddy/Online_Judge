/**
 * Home.jsx — Authenticated landing page
 *
 * A minimal home page shown after the user logs in.
 * Displays the user's name and a logout button.
 * This is a placeholder that will be replaced with the
 * full problem list page in the next implementation phase.
 */

import { useAuth } from '../context/AuthContext';
import './Home.css';

function Home() {
  const { user, logout } = useAuth();

  return (
    <div className="home-page">
      {/* Navigation bar */}
      <nav className="home-nav">
        <div className="home-nav-brand">
          <span className="auth-logo-icon">⚡</span>
          <span className="auth-logo-text" style={{ fontSize: '1.2rem' }}>CodeArena</span>
        </div>
        <div className="home-nav-right">
          <span className="home-nav-user">
            {user?.full_name}
          </span>
          <button onClick={logout} className="btn btn-ghost" style={{ padding: '8px 16px', fontSize: '0.85rem' }}>
            Logout
          </button>
        </div>
      </nav>

      {/* Main content */}
      <main className="home-content fade-in-up">
        <div className="home-welcome-card card">
          <div className="home-welcome-emoji">🎯</div>
          <h1 className="home-welcome-title">
            Welcome, {user?.full_name?.split(' ')[0]}!
          </h1>
          <p className="home-welcome-text">
            You&apos;re all set. The problem list, War Rooms, and discussions
            are coming soon. For now, your authentication is working perfectly.
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
