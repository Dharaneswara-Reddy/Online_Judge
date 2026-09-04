import api from "./axios";

/**
 * assist.js — the AI assist endpoints.
 *
 * Assist is optional on the server: with no provider wired, or with the
 * kill switch off, every one of these routes answers 503. That is not an
 * error the student should ever see — the feature simply is not there —
 * so a 503 is translated into `null` rather than thrown. Callers treat
 * `null` as "hide yourself" and a thrown error as "this one call failed".
 *
 * Nothing here polls. Every function is called from a discrete user
 * action or from a submission reaching a terminal verdict, because each
 * hint, explanation and review costs a model call.
 */

/** A 503 means assist is switched off server-side, not that we failed. */
function isDisabled(err) {
  return err?.response?.status === 503;
}

/**
 * Unwrap the `{ success, message, data }` envelope.
 *
 * Some existing endpoints put their payload at the top level instead of
 * under `data`, so fall back to the body itself rather than handing the
 * caller an empty object.
 */
function payloadOf(body) {
  return body?.data ?? body ?? {};
}

/**
 * Ask whether this student looks stuck on this problem.
 *
 * Returns null when assist is disabled, so the nudge can render nothing
 * at all. Otherwise returns the server's State: whether they are stuck,
 * the sentence explaining why, how many attempts it counted, and the
 * highest hint rung those signals justify.
 */
export async function fetchAssistState(slug) {
  try {
    const { data } = await api.get(`/problems/${slug}/assist/state`);
    const state = payloadOf(data);

    // The server can also report "on, but not for you" with a 200 and
    // enabled:false. Same outcome for the UI as a 503.
    if (state.enabled === false) return null;

    return {
      stuck: Boolean(state.stuck),
      reason: state.reason ?? "",
      attempts: state.attempts ?? 0,
      maxRung: state.maxRung ?? 0,
      lastStatus: state.lastStatus ?? "",
    };
  } catch (err) {
    if (isDisabled(err)) return null;
    throw err;
  }
}

/**
 * Fetch one rung of the hint ladder.
 *
 * `rung` is 1..4 and rises only when the student asks for it a second
 * time; this function never escalates on its own. `submissionId` is
 * optional and only meaningful at rung 3, where the server may look at
 * the hidden case the submission failed — the response still never
 * carries that case.
 */
export async function requestHint({ slug, rung, language, code, submissionId }) {
  try {
    const body = { problemSlug: slug, rung, language, code };
    if (submissionId) body.submissionId = submissionId;

    const { data } = await api.post("/assist/hint", body);
    const hint = payloadOf(data);
    return {
      rung: hint.rung ?? rung,
      text: hint.text ?? "",
      cached: Boolean(hint.cached),
    };
  } catch (err) {
    if (isDisabled(err)) return null;
    throw err;
  }
}

/** Explain a non-accepted verdict in prose. Owner-only, server-enforced. */
export async function explainVerdict(submissionId) {
  try {
    const { data } = await api.post("/assist/explain", { submissionId });
    const explanation = payloadOf(data);
    return { text: explanation.text ?? "", cached: Boolean(explanation.cached) };
  } catch (err) {
    if (isDisabled(err)) return null;
    throw err;
  }
}

/**
 * Review an accepted solution.
 *
 * The server 404s unless the submission was accepted, which is what
 * stops this from becoming a way to read a solution out of a problem
 * you have not solved.
 */
export async function reviewSolution(submissionId) {
  try {
    const { data } = await api.post("/assist/review", { submissionId });
    const review = payloadOf(data);
    return { text: review.text ?? "", cached: Boolean(review.cached) };
  } catch (err) {
    if (isDisabled(err)) return null;
    throw err;
  }
}
