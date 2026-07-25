/**
 * axios.js — HTTP client configuration
 *
 * Creates a pre-configured Axios instance that all API calls
 * in the app use. Key settings:
 *
 * - baseURL points to the Go backend
 * - withCredentials is true so the browser automatically
 *   sends the HTTP-only auth cookie with every request
 * - No token handling needed — cookies are transparent
 */

import axios from 'axios';

// Create an Axios instance with the backend's base URL.
// All API calls in the app should import and use this instance
// instead of the global axios object.
const api = axios.create({
  baseURL: 'http://localhost:8080/api',

  // This is critical for cookie-based auth.
  // Without it, the browser won't send or receive cookies
  // on cross-origin requests (frontend on :5173, backend on :8080).
  withCredentials: true,

  headers: {
    'Content-Type': 'application/json',
  },
});

export default api;
