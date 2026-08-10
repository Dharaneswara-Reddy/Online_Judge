/**
 * App.jsx — Root application component
 *
 * Sets up the client-side routing using react-router-dom.
 * Routes are organized into:
 * - Public routes: /, /problems, /companies, /login, /signup
 * - Protected routes: everything that acts on behalf of a user
 *
 * The landing page, problem list and company explorer are public on
 * purpose: a visitor should be able to see what the platform is before
 * being asked to create an account.
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
import Profile from './pages/Profile';
import Companies from './pages/Companies';
import WarRoomLobby from './pages/WarRoomLobby';
import WarRoomLive from './pages/WarRoomLive';
import Admin from './pages/Admin';
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
        {/* Auth routes — redirect to home if already logged in */}
        <Route
          path="/login"
          element={user ? <Navigate to="/" replace /> : <Login />}
        />
        <Route
          path="/signup"
          element={user ? <Navigate to="/" replace /> : <Signup />}
        />

        {/* Public routes — readable without an account */}
        <Route path="/" element={<Home />} />
        <Route path="/problems" element={<Problems />} />
        <Route path="/problems/:slug" element={<ProblemDetail />} />
        <Route path="/companies" element={<Companies />} />
        <Route path="/companies/:name" element={<Companies />} />

        {/* Protected routes — require authentication */}
        <Route element={<ProtectedRoute />}>
          <Route path="/playground" element={<Playground />} />
          <Route path="/profile" element={<Profile />} />
          <Route path="/warrooms" element={<WarRoomLobby />} />
          <Route path="/warrooms/:code" element={<WarRoomLive />} />
          <Route path="/admin" element={<Admin />} />
        </Route>

        {/* Catch-all — redirect unknown routes to home */}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
