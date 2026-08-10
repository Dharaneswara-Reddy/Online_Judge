import api from "./axios";

/**
 * Fetch the aggregated company tags for a problem, plus which of them
 * the signed-in user reported themselves.
 */
export async function fetchCompanyTags(slug) {
  const { data } = await api.get(`/problems/${slug}/company-tags`);
  return { companies: data.data ?? [], myTags: data.myTags ?? [] };
}

/** Report that a problem was seen at a company. */
export async function tagCompany(slug, { company, round } = {}) {
  const { data } = await api.post(`/problems/${slug}/company-tags`, { company, round });
  return data.data;
}

/** List every company with at least one tag, most-tagged first. */
export async function fetchCompanies() {
  const { data } = await api.get("/companies");
  return data.data ?? [];
}

/** List the problems tagged with one company. */
export async function fetchCompanyProblems(name) {
  const { data } = await api.get(`/companies/${encodeURIComponent(name)}/problems`);
  return data.data ?? [];
}
