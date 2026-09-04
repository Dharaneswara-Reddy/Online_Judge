/**
 * StuckNudge.jsx — the quiet offer.
 *
 * The whole design constraint here is not to interrupt. It is a strip,
 * not a modal; it never takes focus; and it is positioned absolutely
 * over the bottom of the editor so that appearing costs the editor no
 * height and moves nothing the student is looking at.
 *
 * It asks the server whether the student looks stuck at exactly two
 * moments: once when the page opens, and again each time a submission
 * reaches a terminal verdict (the parent bumps `refreshKey`). There is
 * no timer, and nothing here reacts to typing.
 *
 * Dismissing it is permanent for that problem. Someone who says "no"
 * should not be asked again tomorrow.
 */

import { useState, useEffect, useRef } from "react";
import { fetchAssistState } from "../../api/assist";
import "./StuckNudge.css";

const dismissKey = (slug) => `codearena.assist.nudge-dismissed.${slug}`;

/** Private-window localStorage throws on access, so never let it escape. */
function readDismissed(slug) {
  try {
    return window.localStorage.getItem(dismissKey(slug)) === "1";
  } catch {
    // Storage unavailable. The offer stands for this page load only.
    return false;
  }
}

function rememberDismissed(slug) {
  try {
    window.localStorage.setItem(dismissKey(slug), "1");
  } catch {
    // Storage unavailable. The dismissal still holds until reload,
    // which is the best that can be promised here.
  }
}

export default function StuckNudge({ slug, refreshKey = 0, onAskForHint, onDisabled }) {
  const [dismissed, setDismissed] = useState(() => readDismissed(slug));
  const [state, setState] = useState(null);

  // Held in a ref so an inline arrow from the parent cannot re-trigger
  // the effect below on every render — that would turn "one check per
  // verdict" into a request loop.
  const onDisabledRef = useRef(onDisabled);
  useEffect(() => { onDisabledRef.current = onDisabled; });

  // Dismissal is remembered per problem, so moving to another problem
  // means re-reading the flag rather than inheriting this one's.
  useEffect(() => {
    setDismissed(readDismissed(slug));
    setState(null);
  }, [slug]);

  useEffect(() => {
    // Already turned down: there is nothing to show, so do not ask.
    if (dismissed) return;

    let cancelled = false;
    (async () => {
      try {
        const next = await fetchAssistState(slug);
        if (cancelled) return;
        if (next === null) {
          // Assist is switched off server-side. Hide the feature whole
          // rather than showing an error about something the student
          // never asked for.
          onDisabledRef.current?.();
          return;
        }
        setState(next);
      } catch {
        // A failed check is silence. An unsolicited offer that fails is
        // not worth an error banner.
      }
    })();
    return () => { cancelled = true; };
  }, [slug, refreshKey, dismissed]);

  const handleDismiss = () => {
    setDismissed(true);
    rememberDismissed(slug);
  };

  if (dismissed || !state?.stuck) return null;

  return (
    <aside
      className="assist-nudge"
      role="status"
      aria-live="polite"
      onKeyDown={(e) => { if (e.key === "Escape") handleDismiss(); }}
    >
      <p className="assist-nudge-reason">
        {state.reason || "You have been on this one a while."}
      </p>
      <div className="assist-nudge-actions">
        <button
          type="button"
          className="assist-nudge-accept"
          onClick={() => onAskForHint?.(state)}
        >
          Want a hint?
        </button>
        <button
          type="button"
          className="assist-nudge-dismiss"
          onClick={handleDismiss}
          aria-label="No thanks — stop offering hints on this problem"
        >
          No thanks
        </button>
      </div>
    </aside>
  );
}
