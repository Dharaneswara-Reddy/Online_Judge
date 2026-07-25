/**
 * AuthContext.jsx — Authentication state management
 *
 * Provides auth state (user, loading) and actions (login,
 * register, logout) to the entire app via React Context.
 *
 * How it works:
 * - On mount, it calls GET /auth/me to check if the user
 *   has a valid auth cookie from a previous session.
 * - login() calls POST /auth/login — the server sets an
 *   HTTP-only cookie. We never see the token.
 * - register() calls POST /auth/register — the user must
 *   then log in separately (no auto-login on register).
 * - logout() calls POST /auth/logout — the server clears
 *   the cookie.
 */

import { createContext, useContext, useState, useEffect } from 'react';
import api from '../api/axios';

// Create the context — other components will consume this
const AuthContext = createContext(null);

/**
 * useAuth — custom hook for accessing auth state.
 * Components call this instead of useContext(AuthContext) directly.
 *
 * Returns: { user, loading, login, register, logout }
 */
export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used inside an AuthProvider');
  }
  return context;
}

/**
 * AuthProvider — wraps the app and provides auth state.
 * Place this near the top of the component tree (in main.jsx or App.jsx).
 */
export function AuthProvider({ children }) {
  // user is null when not logged in, or an object with user data
  const [user, setUser] = useState(null);

  // loading is true while we're checking the auth cookie on mount
  const [loading, setLoading] = useState(true);

  // ================================================
  // On mount: check if the user is already logged in
  // ================================================
  useEffect(() => {
    checkAuth();
  }, []);

  /**
   * checkAuth — calls GET /auth/me to see if the user has
   * a valid session cookie. If yes, sets the user state.
   * If no (401), sets user to null.
   */
  async function checkAuth() {
    try {
      const response = await api.get('/auth/me');
      setUser(response.data.data.user);
    } catch {
      // 401 or network error — user is not logged in
      setUser(null);
    } finally {
      setLoading(false);
    }
  }

  /**
   * login — authenticates the user with email and password.
   * The server sets an HTTP-only cookie on success.
   *
   * @param {string} email - User's email address
   * @param {string} password - User's password
   * @returns {Object} - The response data from the server
   * @throws {Object} - Axios error if login fails
   */
  async function login(email, password) {
    const response = await api.post('/auth/login', { email, password });
    setUser(response.data.data.user);
    return response.data;
  }

  /**
   * register — creates a new user account.
   * Does NOT auto-login — the user must call login() afterward.
   *
   * @param {Object} data - Registration data (full_name, username, email, password, dob)
   * @returns {Object} - The response data from the server
   * @throws {Object} - Axios error if registration fails
   */
  async function register(data) {
    const response = await api.post('/auth/register', data);
    return response.data;
  }

  /**
   * logout — clears the auth cookie by calling the server.
   * The server sets the cookie's MaxAge to -1, which tells
   * the browser to delete it.
   */
  async function logout() {
    try {
      await api.post('/auth/logout');
    } catch {
      // Even if the server call fails, clear local state
    }
    setUser(null);
  }

  // The value object that all consumers of this context receive
  const value = {
    user,
    loading,
    login,
    register,
    logout,
  };

  return (
    <AuthContext.Provider value={value}>
      {children}
    </AuthContext.Provider>
  );
}
