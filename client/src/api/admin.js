import api from "./axios";

/** Create a problem. Admin only. */
export async function createProblem(problem) {
  const { data } = await api.post("/admin/problems", problem);
  return data.data;
}

/** Update an existing problem. Admin only. */
export async function updateProblem(id, problem) {
  const { data } = await api.put(`/admin/problems/${id}`, problem);
  return data.data;
}

/** List every test case for a problem, hidden ones included. Admin only. */
export async function fetchTestCases(problemId) {
  const { data } = await api.get(`/admin/problems/${problemId}/testcases`);
  return data.data ?? [];
}

/** Add a test case to a problem. Admin only. */
export async function addTestCase(problemId, { input, expectedOutput, isSample }) {
  const { data } = await api.post(`/admin/problems/${problemId}/testcases`, {
    input,
    expectedOutput,
    isSample,
  });
  return data.data;
}
