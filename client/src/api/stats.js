import api from "./axios";

/** Community totals for the landing page. */
export async function fetchSummary() {
  const { data } = await api.get("/stats/summary");
  return data.data;
}

/** The most recently added problems, for the landing page preview. */
export async function fetchRecentProblems(limit = 5) {
  const { data } = await api.get("/problems/recent", { params: { limit } });
  return data.data ?? [];
}

/** The signed-in user's profile together with their solve statistics. */
export async function fetchProfile() {
  const { data } = await api.get("/users/me");
  return data.data;
}

/** Update the signed-in user's own profile fields. */
export async function updateProfile({ full_name, dob } = {}) {
  const { data } = await api.patch("/users/me", { full_name, dob });
  return data.data?.user;
}

/** The signed-in user's solve statistics on their own. */
export async function fetchMyStats() {
  const { data } = await api.get("/users/me/stats");
  return data.data;
}
