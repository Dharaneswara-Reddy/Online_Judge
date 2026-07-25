/**
 * Login.jsx — Login page component
 *
 * A premium dark-themed login form with glassmorphism styling.
 * Uses the AuthContext for authentication and react-router-dom
 * for navigation.
 *
 * Features:
 * - Email + password form with validation
 * - Error display for invalid credentials
 * - Link to the signup page
 * - Fade-in animation on mount
 * - Loading state on the submit button
 */

import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
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
      {/* Background decoration elements */}
      <div className="auth-bg-decoration">
        <div className="auth-bg-orb auth-bg-orb-1"></div>
        <div className="auth-bg-orb auth-bg-orb-2"></div>
        <div className="auth-bg-orb auth-bg-orb-3"></div>
      </div>

      <div className="auth-container fade-in-up">
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
            <label htmlFor="login-email" className="form-label">Email</label>
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
            <label htmlFor="login-password" className="form-label">Password</label>
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
                <span className="spinner" style={{ width: 18, height: 18, borderWidth: 2 }}></span>
                Signing in...
              </>
            ) : (
              'Sign in'
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
  );
}

export default Login;
