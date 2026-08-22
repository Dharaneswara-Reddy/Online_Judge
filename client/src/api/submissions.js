import api from "./axios";

/**
 * Queue a solution for judging.
 *
 * The API answers as soon as the attempt is recorded, so the response
 * usually carries `status: "pending"` rather than a verdict. Callers
 * should follow up with pollSubmission.
 */
export async function submitSolution({ slug, language, code }) {
  const { data } = await api.post(`/problems/${slug}/submit`, { language, code });
  return data;
}

/** Fetch one submission, including its verdict once judging finishes. */
export async function fetchSubmission(id) {
  const { data } = await api.get(`/submissions/${id}`);
  return data;
}

/** Statuses that mean the judge is done with a submission. */
const TERMINAL_STATUSES = new Set([
  "accepted",
  "wrong_answer",
  "tle",
  "mle",
  "runtime_error",
  "compile_error",
  "output_limit_exceeded",
  // The judge itself failed, so the submission never got a verdict. It
  // is terminal all the same: without it here, polling keeps asking
  // about a finished submission until the window runs out and only then
  // shows the result.
  "error",
]);

export function isTerminalStatus(status) {
  return TERMINAL_STATUSES.has(status);
}

/**
 * Poll a submission until the judge reaches a verdict.
 *
 * onUpdate is called with every intermediate state so the UI can show
 * "queued" and then "running" while the worker picks the job up. If the
 * verdict never arrives within the timeout the last known state is
 * returned, leaving the caller to decide how to present it.
 *
 * The timeout deliberately exceeds the server's own evaluation budget.
 * It used to be exactly 60s, the same number the server allows itself,
 * so a submission that legitimately used its full budget had the client
 * give up in the same instant the verdict was being written — and the UI
 * sat on "Queued" forever for a submission that had in fact been judged.
 * The extra headroom covers queue wait and the poll interval so the
 * client sees the terminal state the server did reach.
 */
export async function pollSubmission(id, { onUpdate, intervalMs = 700, timeoutMs = 90000 } = {}) {
  const deadline = Date.now() + timeoutMs;
  let latest = null;

  while (Date.now() < deadline) {
    latest = await fetchSubmission(id);
    onUpdate?.(latest);
    if (isTerminalStatus(latest.status)) return latest;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }

  return latest;
}

/** Fetch the signed-in user's submission history. */
export async function fetchMySubmissions({ status, problemId, page, pageSize } = {}) {
  const params = {};
  if (status) params.status = status;
  if (problemId) params.problemId = problemId;
  if (page) params.page = page;
  if (pageSize) params.pageSize = pageSize;
  const { data } = await api.get("/users/me/submissions", { params });
  return data;
}
