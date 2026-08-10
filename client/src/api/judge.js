import api from "./axios";

export async function runCode({ language, code, stdin }) {
  const { data } = await api.post("/judge/run-raw", { language, code, stdin });
  return data;
}
