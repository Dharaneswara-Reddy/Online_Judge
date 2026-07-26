/**
 * TerminalChrome.jsx — Shared terminal chrome bar component
 *
 * Renders the macOS-style window chrome with three colored
 * traffic-light dots and a monospace tab label showing the
 * current "file path" (e.g. ~/auth/login).
 *
 * The dots are decorative only — no hover/click effects,
 * since adding interaction would imply they control something.
 *
 * @param {string} path — The tab label shown in monospace
 */

import './TerminalChrome.css';

function TerminalChrome({ path }) {
  return (
    <div className="terminal-chrome">
      <span className="chrome-dot chrome-dot--close" />
      <span className="chrome-dot chrome-dot--minimize" />
      <span className="chrome-dot chrome-dot--maximize" />
      <span className="chrome-tab">{path}</span>
    </div>
  );
}

export default TerminalChrome;
