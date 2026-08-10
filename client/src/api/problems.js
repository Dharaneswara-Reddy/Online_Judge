import api from "./axios";

export async function fetchProblems({ difficulty, tag, page, pageSize } = {}) {
  const params = {};
  if (difficulty) params.difficulty = difficulty;
  if (tag) params.tag = tag;
  if (page) params.page = page;
  if (pageSize) params.pageSize = pageSize;
  const { data } = await api.get("/problems", { params });
  return data;
}

export async function fetchProblem(slug) {
  const { data } = await api.get(`/problems/${slug}`);
  return data;
}
