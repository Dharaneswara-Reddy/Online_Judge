/**
 * VerdictInsight.jsx — the offer attached to a verdict.
 *
 * A failed verdict can be explained; an accepted one can be reviewed.
 * Both are offers, never actions: nothing here calls the model until the
 * student clicks, because an automatic call on every verdict would put
 * the API bill in the hands of whoever is submitting fastest.
 *
 * It renders inside the existing verdict banner, wrapping onto its own
 * line, so the banner keeps saying what it always said.
 */

import { useState, useEffect } from "react";
import { isTerminalStatus } from "../../api/submissions";
import { explainVerdict, reviewSolution } from "../../api/assist";
import "./VerdictInsight.css";

/**
 * Prose in, paragraphs out.
 *
 * A post-acceptance review comes back under eight fixed markdown
 * headings, so those are rendered as headings rather than as paragraphs
 * beginning with hashes. Everything else stays prose: this deliberately
 * cannot render a code block, because a review that contained one would
 * be a bug and there is no reason to make it look good.
 */
function InsightProse({ text }) {
  const lines = text.split(/\n+/).map((line) => line.trim()).filter(Boolean);
  return lines.map((line, i) => {
    const heading = line.match(/^#{1,6}\s+(.*)$/);
    if (heading) {
      return <h4 key={i} className="assist-insight-heading">{heading[1]}</h4>;
    }
    return <p key={i} className="assist-insight-prose">{line.replace(/^[-*]\s+/, "\u2022 ")}</p>;
  });
}

export default function VerdictInsight({ submissionId, status, onDisabled }) {
  const [text, setText] = useState(null);
  const [cached, setCached] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [hidden, setHidden] = useState(false);

  // A new verdict is a new question; nothing carries over from the last.
  useEffect(() => {
    setText(null);
    setCached(false);
    setError(null);
    setLoading(false);
  }, [submissionId, status]);

  const accepted = status === "accepted";

  // "error" is the judge failing, not a verdict on the code — there is
  // nothing about it a model could usefully explain.
  const offerable =
    Boolean(submissionId) && isTerminalStatus(status) && status !== "error";

  const handleAsk = async () => {
    if (loading) return;
    setLoading(true);
    setError(null);
    try {
      const result = accepted
        ? await reviewSolution(submissionId)
        : await explainVerdict(submissionId);

      if (result === null) {
        // Assist is off server-side: show nothing at all, here or
        // anywhere else on the page.
        setHidden(true);
        onDisabled?.();
        return;
      }
      setText(result.text);
      setCached(Boolean(result.cached));
    } catch (err) {
      setError(
        err?.response?.data?.message ?? "That could not be fetched right now."
      );
    } finally {
      setLoading(false);
    }
  };

  if (!offerable || hidden) return null;

  return (
    <>
      {text === null && (
        <button
          type="button"
          className="assist-insight-ask"
          onClick={handleAsk}
          disabled={loading}
        >
          {loading
            ? "Thinking…"
            : accepted
              ? "Review my solution"
              : "Explain this verdict"}
        </button>
      )}

      {error && <p className="assist-insight-error">{error}</p>}

      {text !== null && (
        <div className="assist-insight-body">
          {accepted && (
            <p className="assist-insight-note">
              CodeArena&rsquo;s judge decided this submission is correct. What follows is an
              advisory review of how it is written{cached ? ", shown from an earlier read of this same submission" : ""}.
            </p>
          )}
          <InsightProse text={text} />
        </div>
      )}
    </>
  );
}
