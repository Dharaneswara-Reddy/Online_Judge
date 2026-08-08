/**
 * App.jsx — Root application component
 *
 * Sets up the client-side routing using react-router-dom.
 * Routes are organized into:
 * - Public routes: /login, /signup (accessible without auth)
 * - Protected routes: / (requires authentication)
 *
 * The ProtectedRoute component handles redirecting
 * unauthenticated users to the login page.
 */

import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { useAuth } from './context/AuthContext';
import ProtectedRoute from './components/ProtectedRoute';
import Login from './pages/Login';
import Signup from './pages/Signup';
import Home from './pages/Home';
import Playground from './pages/Playground';
import Problems from './pages/Problems';
import ProblemDetail from './pages/ProblemDetail';
import './App.css';

function App() {
  const { user, loading } = useAuth();

  // Show a loading screen while checking auth state
  if (loading) {
    return (
      <div className="loading-screen">
        <div className="spinner spinner-lg"></div>
        <p>Loading...</p>
      </div>
    );
  }

  return (
    <BrowserRouter>
      <Routes>
        {/* Public routes — redirect to home if already logged in */}
        <Route
          path="/login"
          element={user ? <Navigate to="/" replace /> : <Login />}
        />
        <Route
          path="/signup"
          element={user ? <Navigate to="/" replace /> : <Signup />}
        />

        {/* Protected routes — require authentication */}
        <Route element={<ProtectedRoute />}>
          <Route path="/" element={<Home />} />
          <Route path="/playground" element={<Playground />} />
          <Route path="/problems" element={<Problems />} />
          <Route path="/problems/:slug" element={<ProblemDetail />} />
        </Route>

        {/* Catch-all — redirect unknown routes to home */}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
