/**
 * ProtectedRoute.jsx — Route guard component
 *
 * Wraps routes that require authentication. If the user is
 * not logged in (user is null), it redirects to the login page.
 * While the auth state is loading (on initial page load),
 * it shows a loading spinner.
 *
 * Usage in App.jsx:
 *   <Route element={<ProtectedRoute />}>
 *     <Route path="/" element={<Home />} />
 *   </Route>
 */

import { Navigate, Outlet } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';

function ProtectedRoute() {
  const { user, loading } = useAuth();

  // While checking the auth cookie, show a loading screen.
  // This prevents a flash of the login page on refresh.
  if (loading) {
    return (
      <div className="loading-screen">
        <div className="spinner spinner-lg"></div>
        <p>Loading...</p>
      </div>
    );
  }

  // If the user is not authenticated, redirect to login
  if (!user) {
    return <Navigate to="/login" replace />;
  }

  // User is authenticated — render the child routes
  return <Outlet />;
}

export default ProtectedRoute;
