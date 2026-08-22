/**
 * Signup.jsx — Registration page component
 *
 * Styled as a terminal / code editor window matching the
 * Login page. Shares the AuthBackground canvas and
 * TerminalChrome components. Collects full name, username,
 * email, password, and optional DOB.
 *
 * Features:
 * - AuthBackground canvas with interactive dot-field
 * - TerminalChrome bar with "~/auth/register" tab
 * - Full registration form with monospace field labels
 * - Arrow icon slides in on button hover
 * - Inline error messages per field
 * - Server error display
 * - Loading state on submit
 * - Staggered entrance animation on mount
 */

import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import AuthBackground from '../components/auth/AuthBackground';
import TerminalChrome from '../components/ui/TerminalChrome';
import './Auth.css';

function Signup() {
  const navigate = useNavigate();
  const { register } = useAuth();

  // Form field states
  const [fullName, setFullName] = useState('');
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [dob, setDob] = useState('');

  // UI states
  const [errors, setErrors] = useState({});
  const [serverError, setServerError] = useState('');
  const [isLoading, setIsLoading] = useState(false);

  /**
   * validate — checks all form fields and returns an object
   * of field-level error messages. Returns an empty object if
   * all fields are valid.
   */
  function validate() {
    const newErrors = {};

    if (!fullName.trim()) {
      newErrors.fullName = 'Full name is required';
    }

    if (!username.trim()) {
      newErrors.username = 'Username is required';
    } else if (username.trim().length < 3) {
      newErrors.username = 'Username must be at least 3 characters';
    }

    if (!email.trim()) {
      newErrors.email = 'Email is required';
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      newErrors.email = 'Please enter a valid email address';
    }

    if (!password) {
      newErrors.password = 'Password is required';
    } else if (password.length < 8) {
      // Mirrors the server rule in auth_controller.go. This check is for
      // UX only — the backend is the one that enforces it.
      newErrors.password = 'Password must be at least 8 characters';
    } else if (new TextEncoder().encode(password).length > 72) {
      // bcrypt cannot hash more than 72 bytes, and bytes are not
      // characters: accented or non-Latin text hits the ceiling well
      // before 72 keystrokes. Saying so here beats a rejected submit.
      newErrors.password = 'Password is too long (72 bytes maximum)';
    }

    return newErrors;
  }

  /**
   * handleSubmit — processes the signup form submission.
   * Validates all fields, calls register(), and redirects
   * to the login page on success.
   */
  async function handleSubmit(e) {
    e.preventDefault();
    setServerError('');

    // Steps to follow while registering a user
    // ==========================================

    // 1. Validate all form fields
    const validationErrors = validate();
    setErrors(validationErrors);

    if (Object.keys(validationErrors).length > 0) {
      return;
    }

    // 2. Build the registration data payload
    const data = {
      full_name: fullName.trim(),
      username: username.trim(),
      email: email.trim(),
      password: password,
    };

    // Only include DOB if the user filled it in
    if (dob) {
      data.dob = dob;
    }

    // 3. Call the register function from AuthContext
    setIsLoading(true);
    try {
      await register(data);

      // 4. Redirect to login page with a success message
      navigate('/login', { state: { registered: true } });
    } catch (err) {
      // 5. Show the error from the server
      if (err.response?.data?.message) {
        setServerError(err.response.data.message);
      } else if (err.response?.data?.errors) {
        setServerError(err.response.data.errors.join('. '));
      } else {
        setServerError('Something went wrong. Please try again.');
      }
    } finally {
      setIsLoading(false);
    }
  }

  return (
    <div className="auth-page">
      {/* Mouse-reactive dot-field background */}
      <AuthBackground />

      <div className="auth-container auth-container-wide">
        {/* Terminal chrome bar with macOS dots */}
        <TerminalChrome path="~/auth/register" />

        {/* Card body */}
        <div className="auth-body">
          {/* Logo / Brand */}
          <div className="auth-header">
            <div className="auth-logo">
              <span className="auth-logo-icon">⚡</span>
              <span className="auth-logo-text">CodeArena</span>
            </div>
            <h1 className="auth-title">Create your account</h1>
            <p className="auth-subtitle">Join the arena and start solving</p>
          </div>

          {/* Server error alert */}
          {serverError && (
            <div className="alert alert-error">
              {serverError}
            </div>
          )}

          {/* Signup form */}
          <form onSubmit={handleSubmit} className="auth-form">
            <div className="form-row">
              <div className="form-group">
                <label htmlFor="signup-fullname" className="form-label">full_name</label>
                <input
                  id="signup-fullname"
                  type="text"
                  className={`form-input ${errors.fullName ? 'error' : ''}`}
                  placeholder="John Doe"
                  value={fullName}
                  onChange={(e) => setFullName(e.target.value)}
                  autoFocus
                />
                {errors.fullName && <span className="form-error">{errors.fullName}</span>}
              </div>

              <div className="form-group">
                <label htmlFor="signup-username" className="form-label">username</label>
                <input
                  id="signup-username"
                  type="text"
                  className={`form-input ${errors.username ? 'error' : ''}`}
                  placeholder="johndoe"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
                {errors.username && <span className="form-error">{errors.username}</span>}
              </div>
            </div>

            <div className="form-group">
              <label htmlFor="signup-email" className="form-label">email</label>
              <input
                id="signup-email"
                type="email"
                className={`form-input ${errors.email ? 'error' : ''}`}
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
              />
              {errors.email && <span className="form-error">{errors.email}</span>}
            </div>

            <div className="form-row">
              <div className="form-group">
                <label htmlFor="signup-password" className="form-label">password</label>
                <input
                  id="signup-password"
                  type="password"
                  className={`form-input ${errors.password ? 'error' : ''}`}
                  placeholder="Min. 6 characters"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                />
                {errors.password && <span className="form-error">{errors.password}</span>}
              </div>

              <div className="form-group">
                <label htmlFor="signup-dob" className="form-label">dob <span className="text-muted" style={{ fontFamily: 'var(--font-family)' }}>(optional)</span></label>
                <input
                  id="signup-dob"
                  type="date"
                  className="form-input"
                  value={dob}
                  onChange={(e) => setDob(e.target.value)}
                />
              </div>
            </div>

            <button
              type="submit"
              className="btn btn-primary btn-block"
              disabled={isLoading}
            >
              {isLoading ? (
                <>
                  <span className="spinner" style={{ width: 16, height: 16, borderWidth: 2 }}></span>
                  <span>Creating account...</span>
                </>
              ) : (
                <>
                  <span>Create account</span>
                  <svg className="btn-arrow" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="5" y1="12" x2="19" y2="12" />
                    <polyline points="12 5 19 12 12 19" />
                  </svg>
                </>
              )}
            </button>
          </form>

          {/* Link to login */}
          <p className="auth-footer">
            Already have an account?{' '}
            <Link to="/login">Sign in</Link>
          </p>
        </div>
      </div>
    </div>
  );
}

export default Signup;
