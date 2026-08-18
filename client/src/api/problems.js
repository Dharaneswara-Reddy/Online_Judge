import api from "./axios";

export async function fetchProblems({ difficulty, tag, search, page, pageSize } = {}) {
  const params = {};
  if (difficulty) params.difficulty = difficulty;
  if (tag) params.tag = tag;
  // A blank search is left off entirely rather than sent as an empty
  // string, so the request looks the same as an unfiltered listing.
  if (search) params.search = search;
  if (page) params.page = page;
  if (pageSize) params.pageSize = pageSize;
  const { data } = await api.get("/problems", { params });
  return data;
}

export async function fetchProblem(slug) {
  const { data } = await api.get(`/problems/${slug}`);
  return data;
}
