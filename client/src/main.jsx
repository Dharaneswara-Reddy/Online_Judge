/**
 * main.jsx — Application entry point
 *
 * Mounts the React app into the DOM. Wraps the entire
 * application with the AuthProvider so that auth state
 * is available everywhere in the component tree.
 */

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client';
import { AuthProvider } from './context/AuthContext';
import App from './App';
import './index.css';

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <AuthProvider>
      <App />
    </AuthProvider>
  </StrictMode>,
);
