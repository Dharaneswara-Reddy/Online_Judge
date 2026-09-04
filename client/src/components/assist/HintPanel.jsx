/**
 * HintPanel.jsx — the hint ladder.
 *
 * Four rungs, each disclosing more than the last, and none of them
 * disclosing code. The ladder only ever descends when the student
 * explicitly asks it to: opening the panel reveals rung 1, and every
 * rung after that needs its own click on "Show me a little more". That
 * second click is the whole point — productive difficulty is what the
 * student came for, so going deeper has to be a decision, not a default.
 *
 * Each rung is labelled with what it will and will not say, so the
 * decision is an informed one, and the number of hints already taken on
 * this problem is kept in view rather than tucked away.
 */

import { useState, useEffect, useCallback, useRef } from "react";
import { requestHint } from "../../api/assist";
import "./HintPanel.css";

const TOP_RUNG = 4;

// The ladder, in the server's order. `gives`/`withholds` are shown
// before the student commits to a rung, not after.
const RUNGS = [
  {
    rung: 1,
    name: "Check what you were promised",
    gives: "Restates a guarantee the statement already makes.",
    withholds: "Says nothing about how to solve it.",
  },
  {
    rung: 2,
    name: "The shape of the approach",
    gives: "Names the kind of approach this problem wants.",
    withholds: "Does not name the algorithm or its steps.",
  },
  {
    rung: 3,
    name: "Why it fails",
    gives: "Describes a property of the hidden case your code gets wrong.",
    withholds: "Never shows you the case itself.",
  },
  {
    rung: 4,
    name: "The outline",
    gives: "The steps, in prose.",
    withholds: "Still no code — you write that.",
  },
];

const rungInfo = (rung) => RUNGS.find((r) => r.rung === rung);

const usedKey = (slug) => `codearena.assist.hints-used.${slug}`;

/** Private-window localStorage throws on access, so never let it escape. */
function readUsed(slug) {
  try {
    const n = Number.parseInt(window.localStorage.getItem(usedKey(slug)) ?? "", 10);
    return Number.isInteger(n) && n > 0 ? Math.min(n, TOP_RUNG) : 0;
  } catch {
    // Storage unavailable: the count starts fresh for this page load.
    return 0;
  }
}

function rememberUsed(slug, used) {
  try {
    window.localStorage.setItem(usedKey(slug), String(used));
  } catch {
    // Storage unavailable. The count still holds until reload.
  }
}

/**
 * Hint text is prose and is rendered as prose — paragraphs of plain
 * text, never a <pre> and never anything that could pass for a code
 * block. A rung that returned code would be a server bug, and there is
 * deliberately nothing here that would make such a response look good.
 */
function HintProse({ text }) {
  const paragraphs = text.split(/\n+/).map((line) => line.trim()).filter(Boolean);
  return paragraphs.map((line, i) => (
    <p key={i} className="assist-hint-prose">{line}</p>
  ));
}

export default function HintPanel({
  slug,
  language,
  code,
  submissionId,
  maxRung = 0,
  onClose,
  onDisabled,
}) {
  const [revealed, setRevealed] = useState([]);
  const [used, setUsed] = useState(() => readUsed(slug));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  // `maxRung` is how far down the server's stuck signals justify going.
  // With no signals (the student opened the panel themselves) the ladder
  // is simply the full four rungs and the server decides what it will
  // answer.
  const deepest = maxRung >= 1 && maxRung <= TOP_RUNG ? maxRung : TOP_RUNG;
  const nextRung = (revealed.length > 0 ? revealed[revealed.length - 1].rung : 0) + 1;
  const canDescend = nextRung <= deepest;

  const reveal = useCallback(async (rung) => {
    setLoading(true);
    setError(null);
    try {
      const hint = await requestHint({ slug, rung, language, code, submissionId });
      if (hint === null) {
        // Assist is off server-side; the whole feature hides itself.
        onDisabled?.();
        return;
      }
      setRevealed((current) => [...current, { rung, text: hint.text }]);
      setUsed((current) => {
        // Only a rung deeper than any taken before counts as a new hint,
        // so reopening the panel after a reload cannot inflate the tally.
        const next = Math.max(current, rung);
        if (next !== current) rememberUsed(slug, next);
        return next;
      });
    } catch (err) {
      setError(
        err?.response?.data?.message ?? "That hint could not be fetched. Try again in a moment."
      );
    } finally {
      setLoading(false);
    }
  }, [slug, language, code, submissionId, onDisabled]);

  // Opening the panel is itself the first explicit request, so rung 1
  // arrives without a further click. The ref keeps that to exactly once:
  // `code` changes on every keystroke, and `reveal` changes with it.
  const openedRef = useRef(false);
  useEffect(() => {
    if (openedRef.current) return;
    openedRef.current = true;
    reveal(1);
  }, [reveal]);

  const next = rungInfo(nextRung);

  return (
    <section className="assist-hints" aria-label="Hints">
      <header className="assist-hints-head">
        <h3 className="assist-hints-title">Hints</h3>
        <span className="assist-hints-count">
          {used} of {TOP_RUNG} used on this problem
        </span>
        <button
          type="button"
          className="assist-hints-close"
          onClick={onClose}
          aria-label="Close hints"
        >
          ✕
        </button>
      </header>

      <div className="assist-hints-body">
        {revealed.map((hint) => {
          const info = rungInfo(hint.rung);
          return (
            <article key={hint.rung} className="assist-hint">
              <div className="assist-hint-label">
                Hint {hint.rung} — {info?.name}
              </div>
              <HintProse text={hint.text} />
            </article>
          );
        })}

        {loading && <p className="assist-hints-loading">Thinking&hellip;</p>}
        {error && <p className="assist-hints-error">{error}</p>}
      </div>

      {/* The first rung failing would otherwise leave a dead panel. */}
      {!loading && revealed.length === 0 && error && (
        <div className="assist-hints-next">
          <button type="button" className="assist-hints-more" onClick={() => reveal(1)}>
            Try again
          </button>
        </div>
      )}

      {!loading && revealed.length > 0 && (
        canDescend && next ? (
          <div className="assist-hints-next">
            <p className="assist-hints-next-what">
              <strong>Hint {next.rung} — {next.name}.</strong> {next.gives} {next.withholds}
            </p>
            <button
              type="button"
              className="assist-hints-more"
              onClick={() => reveal(next.rung)}
            >
              Show me a little more
            </button>
          </div>
        ) : (
          <p className="assist-hints-floor">
            That is as far as the ladder goes. The rest is yours.
          </p>
        )
      )}

      <p className="assist-hints-disclosure">
        Your code and this problem&rsquo;s statement are sent to an external AI model to
        generate these hints.
      </p>
    </section>
  );
}
