/**
 * Login.jsx — Login page component
 *
 * Styled as a terminal / code editor window with a chrome
 * bar showing macOS traffic-light dots and a tab label.
 * A mouse-reactive dot-field canvas renders behind the card.
 *
 * Features:
 * - AuthBackground canvas with interactive dot-field
 * - TerminalChrome bar with "~/auth/login" tab
 * - Email + password form with monospace labels
 * - Arrow icon slides in on button hover
 * - Error display for invalid credentials
 * - Link to the signup page
 * - Staggered entrance animation on mount
 * - Loading state on the submit button
 */

import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import AuthBackground from '../components/auth/AuthBackground';
import TerminalChrome from '../components/ui/TerminalChrome';
import './Auth.css';

function Login() {
  const navigate = useNavigate();
  const { login } = useAuth();

  // Form field states
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  // UI states
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(false);

  /**
   * handleSubmit — processes the login form submission.
   * Validates inputs, calls the login function from AuthContext,
   * and navigates to the home page on success.
   */
  async function handleSubmit(e) {
    e.preventDefault();
    setError('');

    // Steps to follow while logging in
    // ===================================

    // 1. Validate that both fields are filled
    if (!email.trim() || !password.trim()) {
      setError('Please fill in all fields');
      return;
    }

    // 2. Call the login function and handle the response
    setIsLoading(true);
    try {
      await login(email, password);

      // 3. Navigate to the home page on success
      navigate('/');
    } catch (err) {
      // 4. Show the error message from the server
      if (err.response?.data?.message) {
        setError(err.response.data.message);
      } else {
        setError('Something went wrong. Please try again.');
      }
    } finally {
      setIsLoading(false);
    }
  }

  return (
    <div className="auth-page">
      {/* Mouse-reactive dot-field background */}
      <AuthBackground />

      <div className="auth-container">
        {/* Terminal chrome bar with macOS dots */}
        <TerminalChrome path="~/auth/login" />

        {/* Card body */}
        <div className="auth-body">
          {/* Logo / Brand */}
          <div className="auth-header">
            <div className="auth-logo">
              <span className="auth-logo-icon">⚡</span>
              <span className="auth-logo-text">CodeArena</span>
            </div>
            <h1 className="auth-title">Welcome back</h1>
            <p className="auth-subtitle">Sign in to continue solving problems</p>
          </div>

          {/* Error alert */}
          {error && (
            <div className="alert alert-error">
              {error}
            </div>
          )}

          {/* Login form */}
          <form onSubmit={handleSubmit} className="auth-form">
            <div className="form-group">
              <label htmlFor="login-email" className="form-label">email</label>
              <input
                id="login-email"
                type="email"
                className="form-input"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
                autoFocus
              />
            </div>

            <div className="form-group">
              <label htmlFor="login-password" className="form-label">password</label>
              <input
                id="login-password"
                type="password"
                className="form-input"
                placeholder="••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
              />
            </div>

            <button
              type="submit"
              className="btn btn-primary btn-block"
              disabled={isLoading}
            >
              {isLoading ? (
                <>
                  <span className="spinner" style={{ width: 16, height: 16, borderWidth: 2 }}></span>
                  <span>Signing in...</span>
                </>
              ) : (
                <>
                  <span>Sign in</span>
                  <svg className="btn-arrow" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="5" y1="12" x2="19" y2="12" />
                    <polyline points="12 5 19 12 12 19" />
                  </svg>
                </>
              )}
            </button>
          </form>

          {/* Link to signup */}
          <p className="auth-footer">
            Don&apos;t have an account?{' '}
            <Link to="/signup">Create one</Link>
          </p>
        </div>
      </div>
    </div>
  );
}

export default Login;
