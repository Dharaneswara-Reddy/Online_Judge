/**
 * NavBar.jsx — the application's top navigation.
 *
 * Every page rendered this bar inline, which meant adding a destination
 * involved editing each page in turn. It lives here once instead, and
 * highlights whichever link matches the current route.
 */

import { Link, useLocation } from "react-router-dom";
import { useAuth } from "../../context/AuthContext";
import "./NavBar.css";

const LINKS = [
  { to: "/problems", label: "Problems" },
  { to: "/warrooms", label: "War Rooms" },
  { to: "/companies", label: "Companies" },
  { to: "/playground", label: "▶ Playground" },
];

export default function NavBar() {
  const { user, logout } = useAuth();
  const { pathname } = useLocation();

  // A link is active for its own route and anything nested beneath it,
  // so /problems/two-sum still highlights "Problems".
  const isActive = (to) => pathname === to || pathname.startsWith(`${to}/`);

  return (
    <nav className="nav-bar">
      <div className="nav-brand">
        <span className="nav-brand-mark">⚡</span>
        <Link to="/" className="nav-brand-name">CodeArena</Link>
      </div>

      <div className="nav-links">
        {LINKS.map(({ to, label }) => (
          <Link key={to} to={to} className={`nav-link ${isActive(to) ? "nav-link-active" : ""}`}>
            {label}
          </Link>
        ))}
        {user?.role === "admin" && (
          <Link to="/admin" className={`nav-link ${isActive("/admin") ? "nav-link-active" : ""}`}>
            Admin
          </Link>
        )}
      </div>

      <div className="nav-right">
        {user ? (
          <>
            <Link to="/profile" className="nav-user" title="Your profile">
              {user.full_name || user.username}
            </Link>
            <button onClick={logout} className="btn btn-ghost nav-btn">Logout</button>
          </>
        ) : (
          <>
            <Link to="/login" className="btn btn-ghost nav-btn">Log in</Link>
            <Link to="/signup" className="btn btn-primary nav-btn">Sign up</Link>
          </>
        )}
      </div>
    </nav>
  );
}
